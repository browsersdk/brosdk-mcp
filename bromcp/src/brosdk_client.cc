#include "bromcp/brosdk_client.h"

#include <cstdlib>
#include <cstring>
#include <sstream>

#include "bromcp/dlfcn_compat.h"
#include "bromcp/json.h"

namespace bromcp {
namespace {

std::string LastLoadError() {
  const char *error = dlerror();
  return error ? std::string(error) : std::string();
}

std::string GetEnv(const char *name) {
#if defined(_WIN32)
  char *value = nullptr;
  size_t len = 0;
  if (_dupenv_s(&value, &len, name) != 0 || !value) {
    return {};
  }
  std::string result(value, value + std::strlen(value));
  free(value);
  return result;
#else
  const char *value = std::getenv(name);
  return value ? std::string(value) : std::string();
#endif
}

} // namespace

BrosdkClient::BrosdkClient() {
  default_library_path_ = GetEnv("BROSDK_LIBRARY");
  if (default_library_path_.empty()) {
    default_library_path_ = DefaultLibraryName();
  }
}

BrosdkClient::~BrosdkClient() { CloseLibrary(); }

bool BrosdkClient::Load(const std::string &library_path, std::string *error) {
  std::lock_guard<std::mutex> lock(mutex_);
  if (module_) {
    return true;
  }

  const std::string path =
      library_path.empty() ? default_library_path_ : library_path;
  module_ = dlopen(path.c_str(), RTLD_NOW | RTLD_LOCAL);

  if (!module_) {
    if (error) {
      *error = "failed to load " + path + ": " + LastLoadError();
    }
    return false;
  }

  loaded_library_path_ = path;
  if (!ResolveRequiredSymbols(error)) {
    CloseLibrary();
    return false;
  }

  return true;
}

bool BrosdkClient::EnsureLoaded(std::string *error) {
  return Load({}, error);
}

bool BrosdkClient::IsLoaded() const {
  std::lock_guard<std::mutex> lock(mutex_);
  return module_ != nullptr;
}

void BrosdkClient::SetDefaultLibraryPath(std::string library_path) {
  std::lock_guard<std::mutex> lock(mutex_);
  if (!library_path.empty()) {
    default_library_path_ = std::move(library_path);
  }
}

std::string BrosdkClient::LibraryPath() const {
  std::lock_guard<std::mutex> lock(mutex_);
  return loaded_library_path_.empty() ? default_library_path_
                                      : loaded_library_path_;
}

void BrosdkClient::SetEventHandler(EventHandler handler) {
  std::lock_guard<std::mutex> lock(mutex_);
  event_handler_ = std::move(handler);
}

bool BrosdkClient::RegisterResultCallback(std::string *error) {
  if (!EnsureSdk(error)) {
    return false;
  }

  ISDK *sdk = nullptr;
  {
    std::lock_guard<std::mutex> lock(mutex_);
    if (callback_registered_) {
      return true;
    }
    sdk = sdk_;
  }

  if (!sdk) {
    if (error) {
      *error = "brosdk C++ interface is unavailable";
    }
    return false;
  }

  const int32_t code =
      sdk->RegisterResultCb(&BrosdkClient::StaticResultCallback, this);
  if (code < 0) {
    if (error) {
      *error = "ISDK::RegisterResultCb failed: " + ErrorMessage(code);
    }
    return false;
  }

  std::lock_guard<std::mutex> lock(mutex_);
  callback_registered_ = true;
  return true;
}

bool BrosdkClient::CallbackRegistered() const {
  std::lock_guard<std::mutex> lock(mutex_);
  return callback_registered_;
}

SdkCallResult BrosdkClient::Init(const std::string &payload_json) {
  std::string error;
  if (!EnsureSdk(&error)) {
    SdkCallResult result;
    result.code = -1;
    result.error_message = error;
    return result;
  }
  RegisterResultCallback(nullptr);

  char *out = nullptr;
  size_t out_len = 0;
  const int32_t code =
      Sdk()->Init(payload_json.data(), payload_json.size(), &out, &out_len);
  SdkCallResult result = CompleteResult(code, out, out_len);
  return result;
}

SdkCallResult BrosdkClient::BrowserInstall(const std::string &payload_json) {
  return CallAsync(&ISDK::BrowserInstall, payload_json);
}

SdkCallResult BrosdkClient::BrowserOpen(const std::string &payload_json) {
  return CallAsync(&ISDK::BrowserOpen, payload_json);
}

SdkCallResult BrosdkClient::BrowserClose(const std::string &payload_json) {
  return CallAsync(&ISDK::BrowserClose, payload_json);
}

SdkCallResult BrosdkClient::TokenUpdate(const std::string &payload_json) {
  return CallAsync(&ISDK::UpdateToken, payload_json);
}

SdkCallResult BrosdkClient::Info() { return CallOutput(&ISDK::Info); }

SdkCallResult BrosdkClient::BrowserInfo() {
  return CallOutput(&ISDK::BrowserInfo);
}

SdkCallResult BrosdkClient::NetworkDiagnostics(const std::string &payload_json) {
  return CallSync(&ISDK::NetworkDiagnostics, payload_json);
}

SdkCallResult BrosdkClient::CreateEnv(const std::string &payload_json) {
  return CallSync(&ISDK::CreateEnv, payload_json);
}

SdkCallResult BrosdkClient::UpdateEnv(const std::string &payload_json) {
  return CallSync(&ISDK::UpdateEnv, payload_json);
}

SdkCallResult BrosdkClient::PageEnv(const std::string &payload_json) {
  return CallSync(&ISDK::PageEnv, payload_json);
}

SdkCallResult BrosdkClient::GetEnvInfo(const std::string &payload_json) {
  return CallSync(&ISDK::GetEnvInfo, payload_json);
}

SdkCallResult BrosdkClient::DestroyEnv(const std::string &payload_json) {
  return CallSync(&ISDK::DestroyEnv, payload_json);
}

SdkCallResult BrosdkClient::Shutdown() {
  std::string error;
  if (!EnsureSdk(&error)) {
    SdkCallResult result;
    result.code = -1;
    result.error_message = error;
    return result;
  }

  const int32_t code = Sdk()->Shutdown();
  SdkCallResult result = CompleteResult(code, nullptr, 0);
  if (result.ok) {
    std::lock_guard<std::mutex> lock(mutex_);
    sdk_ = nullptr;
    callback_registered_ = false;
  }
  return result;
}

std::string BrosdkClient::ErrorName(int32_t code) const {
  if (sdk_error_name_) {
    if (const char *name = sdk_error_name_(code)) {
      return name;
    }
  }
  return std::to_string(code);
}

std::string BrosdkClient::ErrorMessage(int32_t code) const {
  if (sdk_error_string_) {
    if (const char *message = sdk_error_string_(code)) {
      return message;
    }
  }
  return ErrorName(code);
}

std::string BrosdkClient::EventName(int32_t code) const {
  if (sdk_event_name_) {
    if (const char *name = sdk_event_name_(code)) {
      return name;
    }
  }
  return std::to_string(code);
}

bool BrosdkClient::IsAcceptedCode(int32_t code) const {
  return (sdk_is_done_ && sdk_is_done_(code)) ||
         (sdk_is_reqid_ && sdk_is_reqid_(code));
}

bool BrosdkClient::IsOkCode(int32_t code) const {
  return sdk_is_ok_ ? sdk_is_ok_(code) : code == 0;
}

std::string BrosdkClient::DefaultLibraryName() {
#if defined(_WIN32)
  return "brosdk.dll";
#elif defined(__APPLE__)
  return "brosdk.dylib";
#else
  return "brosdk.so";
#endif
}

template <typename T>
bool BrosdkClient::Resolve(T &target, const char *name, std::string *error) {
  target = reinterpret_cast<T>(dlsym(module_, name));
  if (!target) {
    if (error) {
      *error = std::string("missing required symbol ") + name + ": " +
               LastLoadError();
    }
    return false;
  }
  return true;
}

bool BrosdkClient::ResolveRequiredSymbols(std::string *error) {
  return Resolve(sdk_init_cpp_, "sdk_init_cpp", error) &&
         Resolve(sdk_free_, "sdk_free", error) &&
         Resolve(sdk_error_name_, "sdk_error_name", error) &&
         Resolve(sdk_error_string_, "sdk_error_string", error) &&
         Resolve(sdk_event_name_, "sdk_event_name", error) &&
         Resolve(sdk_is_reqid_, "sdk_is_reqid", error) &&
         Resolve(sdk_is_done_, "sdk_is_done", error) &&
         Resolve(sdk_is_ok_, "sdk_is_ok", error);
}

bool BrosdkClient::EnsureSdk(std::string *error) {
  if (!EnsureLoaded(error)) {
    return false;
  }

  {
    std::lock_guard<std::mutex> lock(mutex_);
    if (sdk_) {
      return true;
    }
  }

  sdk_handle_t handle = nullptr;
  const int32_t code = sdk_init_cpp_(&handle);
  if (!IsOkCode(code) || !handle) {
    if (error) {
      *error = "sdk_init_cpp failed: " + ErrorMessage(code);
    }
    return false;
  }

  std::lock_guard<std::mutex> lock(mutex_);
  sdk_ = static_cast<ISDK *>(handle);
  return true;
}

ISDK *BrosdkClient::Sdk() {
  std::lock_guard<std::mutex> lock(mutex_);
  return sdk_;
}

SdkCallResult BrosdkClient::CompleteResult(int32_t code, char *out,
                                           size_t out_len) {
  SdkCallResult result;
  result.code = code;
  result.ok = IsOkCode(code);
  result.accepted = IsAcceptedCode(code);
  result.error_name = ErrorName(code);
  result.error_message = ErrorMessage(code);

  if (out && out_len > 0) {
    result.response_json.assign(out, out + out_len);
  }

  if (out && sdk_free_) {
    sdk_free_(out);
  }

  return result;
}

SdkCallResult BrosdkClient::CallAsync(async_method method,
                                      const std::string &payload_json) {
  std::string error;
  if (!EnsureSdk(&error)) {
    SdkCallResult result;
    result.code = -1;
    result.error_message = error;
    return result;
  }
  RegisterResultCallback(nullptr);
  ISDK *sdk = Sdk();
  const int32_t code = sdk ? (sdk->*method)(payload_json.data(),
                                            payload_json.size())
                           : -1;
  return CompleteResult(code, nullptr, 0);
}

SdkCallResult BrosdkClient::CallOutput(output_method method) {
  std::string error;
  if (!EnsureSdk(&error)) {
    SdkCallResult result;
    result.code = -1;
    result.error_message = error;
    return result;
  }

  char *out = nullptr;
  size_t out_len = 0;
  ISDK *sdk = Sdk();
  const int32_t code = sdk ? (sdk->*method)(&out, &out_len) : -1;
  return CompleteResult(code, out, out_len);
}

SdkCallResult BrosdkClient::CallSync(sync_method method,
                                     const std::string &payload_json) {
  std::string error;
  if (!EnsureSdk(&error)) {
    SdkCallResult result;
    result.code = -1;
    result.error_message = error;
    return result;
  }

  char *out = nullptr;
  size_t out_len = 0;
  ISDK *sdk = Sdk();
  const int32_t code =
      sdk ? (sdk->*method)(payload_json.data(), payload_json.size(), &out,
                           &out_len)
          : -1;
  return CompleteResult(code, out, out_len);
}

void BrosdkClient::CloseLibrary() {
  if (!module_) {
    return;
  }
  if (sdk_) {
    (void)sdk_->Shutdown();
  }
  sdk_ = nullptr;
  dlclose(module_);
  module_ = nullptr;
  callback_registered_ = false;
}

void BrosdkClient::HandleResultCallback(int32_t code, const char *data,
                                        size_t len) {
  EventHandler handler;
  {
    std::lock_guard<std::mutex> lock(mutex_);
    handler = event_handler_;
  }
  if (!handler) {
    return;
  }

  std::string payload;
  if (data && len > 0) {
    payload.assign(data, data + len);
  }
  handler(code, EventName(code), std::move(payload));
}

void SDK_CALL BrosdkClient::StaticResultCallback(int32_t code, void *user_data,
                                                 const char *data,
                                                 size_t len) {
  auto *self = static_cast<BrosdkClient *>(user_data);
  if (!self) {
    return;
  }
  self->HandleResultCallback(code, data, len);
}

} // namespace bromcp
