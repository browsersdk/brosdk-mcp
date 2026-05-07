#pragma once

#include <cstdint>
#include <functional>
#include <mutex>
#include <string>

#include "brosdk.h"

namespace bromcp {

struct SdkCallResult {
  int32_t code = 0;
  bool accepted = false;
  bool ok = false;
  std::string response_json;
  std::string error_name;
  std::string error_message;
};

class BrosdkClient {
public:
  using EventHandler =
      std::function<void(int32_t code, std::string event_name,
                         std::string payload_json)>;

  BrosdkClient();
  ~BrosdkClient();

  BrosdkClient(const BrosdkClient &) = delete;
  BrosdkClient &operator=(const BrosdkClient &) = delete;

  bool Load(const std::string &library_path, std::string *error = nullptr);
  bool EnsureLoaded(std::string *error = nullptr);
  bool IsLoaded() const;

  void SetDefaultLibraryPath(std::string library_path);
  std::string LibraryPath() const;

  void SetEventHandler(EventHandler handler);
  bool RegisterResultCallback(std::string *error = nullptr);
  bool CallbackRegistered() const;

  SdkCallResult Init(const std::string &payload_json);
  SdkCallResult BrowserInstall(const std::string &payload_json);
  SdkCallResult BrowserOpen(const std::string &payload_json);
  SdkCallResult BrowserClose(const std::string &payload_json);
  SdkCallResult TokenUpdate(const std::string &payload_json);
  SdkCallResult Info();
  SdkCallResult BrowserInfo();
  SdkCallResult NetworkDiagnostics(const std::string &payload_json);
  SdkCallResult CreateEnv(const std::string &payload_json);
  SdkCallResult UpdateEnv(const std::string &payload_json);
  SdkCallResult PageEnv(const std::string &payload_json);
  SdkCallResult GetEnvInfo(const std::string &payload_json);
  SdkCallResult DestroyEnv(const std::string &payload_json);
  SdkCallResult Shutdown();

  std::string ErrorName(int32_t code) const;
  std::string ErrorMessage(int32_t code) const;
  std::string EventName(int32_t code) const;
  bool IsAcceptedCode(int32_t code) const;
  bool IsOkCode(int32_t code) const;

  static std::string DefaultLibraryName();

private:
  using init_cpp_fn = int32_t(SDK_CALL *)(sdk_handle_t *);
  using free_fn = void(SDK_CALL *)(void *);
  using code_name_fn = const char *(SDK_CALL *)(int32_t);
  using code_predicate_fn = bool(SDK_CALL *)(int32_t);
  using async_method = int32_t (ISDK::*)(const char *, size_t) const;
  using output_method = int32_t (ISDK::*)(char **, size_t *) const;
  using sync_method = int32_t (ISDK::*)(const char *, size_t, char **,
                                        size_t *) const;

  template <typename T> bool Resolve(T &target, const char *name,
                                     std::string *error);

  bool ResolveRequiredSymbols(std::string *error);
  bool EnsureSdk(std::string *error = nullptr);
  ISDK *Sdk();
  SdkCallResult CompleteResult(int32_t code, char *out, size_t out_len);
  SdkCallResult CallAsync(async_method method,
                          const std::string &payload_json);
  SdkCallResult CallOutput(output_method method);
  SdkCallResult CallSync(sync_method method, const std::string &payload_json);
  void CloseLibrary();
  void HandleResultCallback(int32_t code, const char *data, size_t len);

  static void SDK_CALL StaticResultCallback(int32_t code, void *user_data,
                                            const char *data, size_t len);

  mutable std::mutex mutex_;
  std::string default_library_path_;
  std::string loaded_library_path_;
  bool callback_registered_ = false;
  EventHandler event_handler_;

  void *module_ = nullptr;

  ISDK *sdk_ = nullptr;
  init_cpp_fn sdk_init_cpp_ = nullptr;
  free_fn sdk_free_ = nullptr;
  code_name_fn sdk_error_name_ = nullptr;
  code_name_fn sdk_error_string_ = nullptr;
  code_name_fn sdk_event_name_ = nullptr;
  code_predicate_fn sdk_is_reqid_ = nullptr;
  code_predicate_fn sdk_is_done_ = nullptr;
  code_predicate_fn sdk_is_ok_ = nullptr;
};

} // namespace bromcp
