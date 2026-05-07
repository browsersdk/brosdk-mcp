#include "bromcp/tools.h"

#include <array>
#include <string>
#include <string_view>

#include "bromcp/json.h"

namespace bromcp {
namespace {

constexpr size_t kMaxEnvCount = 16;
constexpr size_t kMaxUrlsPerEnv = 8;
constexpr size_t kMaxArgsPerEnv = 16;

const char kEmptySchema[] = R"({
  "type": "object",
  "additionalProperties": true
})";

const char kInitSchema[] = R"({
  "type": "object",
  "properties": {
    "libraryPath": {"type": "string"},
    "payload": {"type": ["object", "string"]},
    "userSig": {"type": "string"},
    "workDir": {"type": "string"},
    "port": {"type": "integer"},
    "debug": {"type": "boolean"}
  },
  "additionalProperties": true
})";

const char kOpenSchema[] = R"({
  "type": "object",
  "properties": {
    "payload": {"type": ["object", "string"]},
    "envs": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["envId"],
        "properties": {
          "envId": {"type": "string"},
          "urls": {"type": "array", "items": {"type": "string"}},
          "args": {"type": "array", "items": {"type": "string"}},
          "proxy": {"type": "string"},
          "forward": {"type": "string"},
          "bridgeProxy": {"type": "string"},
          "whiteList": {"type": "array", "items": {"type": "string"}},
          "blackList": {"type": "array", "items": {"type": "string"}}
        },
        "additionalProperties": true
      }
    }
  },
  "additionalProperties": true
})";

const char kCloseSchema[] = R"({
  "type": "object",
  "properties": {
    "payload": {"type": ["object", "string"]},
    "envs": {
      "type": "array",
      "items": {"type": "string"}
    }
  },
  "additionalProperties": true
})";

const char kPayloadSchema[] = R"({
  "type": "object",
  "properties": {
    "payload": {"type": ["object", "string"]}
  },
  "additionalProperties": true
})";

const char kDestroyEnvSchema[] = R"({
  "type": "object",
  "properties": {
    "payload": {"type": ["object", "string"]},
    "confirmDestroy": {"type": "boolean"}
  },
  "additionalProperties": true
})";

const char kTaskSchema[] = R"({
  "type": "object",
  "required": ["taskId"],
  "properties": {
    "taskId": {"type": "string"}
  },
  "additionalProperties": false
})";

const char kTokenSchema[] = R"({
  "type": "object",
  "required": ["userSig"],
  "properties": {
    "userSig": {"type": "string"}
  },
  "additionalProperties": false
})";

const char kNetdiagSchema[] = R"({
  "type": "object",
  "required": ["url"],
  "properties": {
    "url": {"type": "string"},
    "proxy": {"type": "string"},
    "bridgeProxy": {"type": "string"}
  },
  "additionalProperties": false
})";

const char kEnvGetInfoSchema[] = R"({
  "type": "object",
  "required": ["envId"],
  "properties": {
    "envId": {"type": "string"},
    "os": {"type": "string"}
  },
  "additionalProperties": false
})";

bool IsBadUrl(std::string_view url) {
  return url.rfind("file:", 0) == 0 || url.rfind("data:", 0) == 0 ||
         url.rfind("javascript:", 0) == 0;
}

bool IsAllowedArg(std::string_view arg) {
  static constexpr std::array<std::string_view, 7> kAllowed = {
      "--lang=", "--window-size=", "--start-maximized", "--disable-gpu",
      "--disable-extensions", "--enable-logging", "--v="};
  for (auto prefix : kAllowed) {
    if (arg == prefix || arg.rfind(prefix, 0) == 0) {
      return true;
    }
  }
  return false;
}

ToolResult ValidateOpenPayload(const std::string &payload_json) {
  rapidjson::Document doc;
  std::string error;
  if (!json::Parse(payload_json, doc, &error) || !doc.IsObject()) {
    return MakeToolError("INVALID_JSON", "invalid open payload: " + error);
  }
  if (!doc.HasMember("envs") || !doc["envs"].IsArray() ||
      doc["envs"].Empty()) {
    return MakeToolError("INVALID_ENVS", "open payload requires envs array");
  }
  if (doc["envs"].Size() > kMaxEnvCount) {
    return MakeToolError("TOO_MANY_ENVS", "too many envs in one call");
  }

  for (const auto &env : doc["envs"].GetArray()) {
    if (!env.IsObject() || !env.HasMember("envId") ||
        !env["envId"].IsString()) {
      return MakeToolError("INVALID_ENVID", "each env must have string envId");
    }
    if (env.HasMember("urls")) {
      if (!env["urls"].IsArray() || env["urls"].Size() > kMaxUrlsPerEnv) {
        return MakeToolError("INVALID_URLS", "urls must be a bounded array");
      }
      for (const auto &url : env["urls"].GetArray()) {
        if (!url.IsString()) {
          return MakeToolError("INVALID_URL", "url entries must be strings");
        }
        if (IsBadUrl({url.GetString(), url.GetStringLength()})) {
          return MakeToolError("URL_REJECTED", "unsafe URL scheme rejected");
        }
      }
    }
    if (env.HasMember("args")) {
      if (!env["args"].IsArray() || env["args"].Size() > kMaxArgsPerEnv) {
        return MakeToolError("INVALID_ARGS", "args must be a bounded array");
      }
      for (const auto &arg : env["args"].GetArray()) {
        if (!arg.IsString() ||
            !IsAllowedArg({arg.GetString(), arg.GetStringLength()})) {
          return MakeToolError("ARG_REJECTED",
                               "browser arg is not in the allowlist");
        }
      }
    }
  }
  return {true, "{}"};
}

ToolResult ValidateClosePayload(const std::string &payload_json) {
  rapidjson::Document doc;
  std::string error;
  if (!json::Parse(payload_json, doc, &error) || !doc.IsObject()) {
    return MakeToolError("INVALID_JSON", "invalid close payload: " + error);
  }
  if (!doc.HasMember("envs") || !doc["envs"].IsArray() ||
      doc["envs"].Empty()) {
    return MakeToolError("INVALID_ENVS", "close payload requires envs array");
  }
  if (doc["envs"].Size() > kMaxEnvCount) {
    return MakeToolError("TOO_MANY_ENVS", "too many envs in one call");
  }
  for (const auto &env_id : doc["envs"].GetArray()) {
    if (!env_id.IsString()) {
      return MakeToolError("INVALID_ENVID", "close envs must be string ids");
    }
  }
  return {true, "{}"};
}

ToolResult ValidateObjectPayload(const std::string &payload_json,
                                 const std::string &operation) {
  rapidjson::Document doc;
  std::string error;
  if (!json::Parse(payload_json, doc, &error) || !doc.IsObject()) {
    return MakeToolError("INVALID_JSON",
                         "invalid " + operation + " payload: " + error);
  }
  return {true, "{}"};
}

std::string PayloadFromParams(const rapidjson::Value &params,
                              const char *const *omitted_keys,
                              size_t omitted_count) {
  if (params.IsObject() && params.HasMember("payload")) {
    const auto &payload = params["payload"];
    if (payload.IsString()) {
      return {payload.GetString(), payload.GetStringLength()};
    }
    return json::Stringify(payload);
  }
  return json::BuildObjectWithoutKeys(params, omitted_keys, omitted_count);
}

std::string SdkCallJson(const SdkCallResult &call, bool ok,
                        const std::string &message) {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  json::AddBool(doc, "ok", ok, alloc);
  json::AddInt(doc, "code", call.code, alloc);
  json::AddBool(doc, "accepted", call.accepted, alloc);
  json::AddString(doc, "errorName", call.error_name, alloc);
  json::AddString(doc, "message", message.empty() ? call.error_message : message,
                  alloc);

  if (!call.response_json.empty()) {
    rapidjson::Document response_doc;
    const std::string redacted = json::RedactJsonText(call.response_json);
    if (json::Parse(redacted, response_doc)) {
      rapidjson::Value copied;
      copied.CopyFrom(response_doc, alloc);
      doc.AddMember("sdkResponse", copied, alloc);
    } else {
      json::AddString(doc, "sdkResponse", redacted, alloc);
    }
  }
  return json::StringifyDocument(doc);
}

ToolResult AcceptedTaskResult(TaskStore &tasks, const std::string &tool_name,
                              const SdkCallResult &call,
                              const std::string &message) {
  if (!call.accepted && !call.ok) {
    return {false, SdkCallJson(call, false, call.error_message)};
  }

  const std::string sdk_req_id =
      call.code > 100000 ? std::to_string(call.code) : std::string();
  TaskRecord task = tasks.CreateAccepted(tool_name, sdk_req_id, message);

  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  json::AddBool(doc, "ok", true, alloc);
  json::AddString(doc, "taskId", task.task_id, alloc);
  json::AddString(doc, "sdkReqId", task.source_req_id, alloc);
  json::AddString(doc, "status", task.status, alloc);
  json::AddString(doc, "message", message, alloc);
  json::AddInt(doc, "code", call.code, alloc);
  json::AddBool(doc, "accepted", call.accepted || call.ok, alloc);
  return {true, json::StringifyDocument(doc)};
}

} // namespace

void RegisterBroTools(ToolRegistry &registry, BrosdkClient &client,
                      TaskStore &tasks) {
  registry.Register(
      {"brosdk_health_check", "Report bromcp and BroSDK adapter health.",
       kEmptySchema},
      [&client](const rapidjson::Value &params) -> ToolResult {
        const bool load = json::GetBool(params, "load", false);
        std::string error;
        bool loaded = client.IsLoaded();
        if (load && !loaded) {
          loaded = client.EnsureLoaded(&error);
        }

        rapidjson::Document doc(rapidjson::kObjectType);
        auto &alloc = doc.GetAllocator();
        json::AddBool(doc, "ok", true, alloc);
        json::AddBool(doc, "loaded", loaded, alloc);
        json::AddString(doc, "libraryPath", client.LibraryPath(), alloc);
        json::AddBool(doc, "callbackRegistered", client.CallbackRegistered(),
                      alloc);
        if (!error.empty()) {
          json::AddString(doc, "loadError", error, alloc);
        }
        return {true, json::StringifyDocument(doc)};
      });

  registry.Register(
      {"brosdk_init", "Initialize BroSDK with a userSig/workDir/port payload.",
       kInitSchema},
      [&client](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string library_path = json::GetString(params, "libraryPath");
        if (!library_path.empty()) {
          client.SetDefaultLibraryPath(library_path);
        }
        static const char *const omitted[] = {"libraryPath"};
        const std::string payload = PayloadFromParams(params, omitted, 1);
        SdkCallResult call = client.Init(payload);
        return {call.ok || call.accepted,
                SdkCallJson(call, call.ok || call.accepted, "sdk init called")};
      });

  registry.Register(
      {"brosdk_browser_install",
       "Install BroSDK browser core resources and track async progress.",
       kPayloadSchema},
      [&client, &tasks](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string payload = PayloadFromParams(params, nullptr, 0);
        ToolResult validation = ValidateObjectPayload(payload, "browser install");
        if (!validation.ok) {
          return validation;
        }
        return AcceptedTaskResult(tasks, "brosdk_browser_install",
                                  client.BrowserInstall(payload),
                                  "browser install task accepted");
      });

  registry.Register(
      {"brosdk_open_browser_envs",
       "Open one or more browser environments with bounded safe inputs.",
       kOpenSchema},
      [&client, &tasks](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string payload = PayloadFromParams(params, nullptr, 0);
        ToolResult validation = ValidateOpenPayload(payload);
        if (!validation.ok) {
          return validation;
        }
        return AcceptedTaskResult(tasks, "brosdk_open_browser_envs",
                                  client.BrowserOpen(payload),
                                  "browser open task accepted");
      });

  registry.Register(
      {"brosdk_close_browser_envs", "Close one or more browser environments.",
       kCloseSchema},
      [&client, &tasks](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string payload = PayloadFromParams(params, nullptr, 0);
        ToolResult validation = ValidateClosePayload(payload);
        if (!validation.ok) {
          return validation;
        }
        return AcceptedTaskResult(tasks, "brosdk_close_browser_envs",
                                  client.BrowserClose(payload),
                                  "browser close task accepted");
      });

  registry.Register(
      {"brosdk_get_env_info",
       "Return SDK and running browser environment summary information.",
       kEmptySchema},
      [&client](const rapidjson::Value &) -> ToolResult {
        SdkCallResult sdk_info = client.Info();
        SdkCallResult browser_info = client.BrowserInfo();

        rapidjson::Document doc(rapidjson::kObjectType);
        auto &alloc = doc.GetAllocator();
        const bool ok = sdk_info.ok && browser_info.ok;
        json::AddBool(doc, "ok", ok, alloc);

        rapidjson::Document sdk_doc;
        const std::string redacted_sdk = json::RedactJsonText(sdk_info.response_json);
        if (json::Parse(redacted_sdk, sdk_doc)) {
          rapidjson::Value copied;
          copied.CopyFrom(sdk_doc, alloc);
          doc.AddMember("sdk", copied, alloc);
        }

        rapidjson::Document browser_doc;
        const std::string redacted_browser =
            json::RedactJsonText(browser_info.response_json);
        if (json::Parse(redacted_browser, browser_doc)) {
          rapidjson::Value copied;
          copied.CopyFrom(browser_doc, alloc);
          doc.AddMember("browsers", copied, alloc);
        }

        return {ok, json::StringifyDocument(doc)};
      });

  registry.Register(
      {"brosdk_create_env", "Create a BroSDK browser environment.",
       kPayloadSchema},
      [&client](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string payload = PayloadFromParams(params, nullptr, 0);
        ToolResult validation = ValidateObjectPayload(payload, "env create");
        if (!validation.ok) {
          return validation;
        }
        SdkCallResult call = client.CreateEnv(payload);
        return {call.ok, SdkCallJson(call, call.ok, "env create called")};
      });

  registry.Register(
      {"brosdk_update_env", "Update a BroSDK browser environment.",
       kPayloadSchema},
      [&client](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string payload = PayloadFromParams(params, nullptr, 0);
        ToolResult validation = ValidateObjectPayload(payload, "env update");
        if (!validation.ok) {
          return validation;
        }
        SdkCallResult call = client.UpdateEnv(payload);
        return {call.ok, SdkCallJson(call, call.ok, "env update called")};
      });

  registry.Register(
      {"brosdk_page_env", "Query BroSDK browser environments.",
       kPayloadSchema},
      [&client](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string payload = PayloadFromParams(params, nullptr, 0);
        ToolResult validation = ValidateObjectPayload(payload, "env page");
        if (!validation.ok) {
          return validation;
        }
        SdkCallResult call = client.PageEnv(payload);
        return {call.ok, SdkCallJson(call, call.ok, "env page called")};
      });

  registry.Register(
      {"brosdk_destroy_env",
       "Destroy a BroSDK browser environment after explicit confirmation.",
       kDestroyEnvSchema},
      [&client](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        if (!json::GetBool(params, "confirmDestroy", false)) {
          return MakeToolError("CONFIRMATION_REQUIRED",
                               "confirmDestroy must be true");
        }
        static const char *const omitted[] = {"confirmDestroy"};
        const std::string payload = PayloadFromParams(params, omitted, 1);
        ToolResult validation = ValidateObjectPayload(payload, "env destroy");
        if (!validation.ok) {
          return validation;
        }
        SdkCallResult call = client.DestroyEnv(payload);
        return {call.ok, SdkCallJson(call, call.ok, "env destroy called")};
      });

  registry.Register(
      {"brosdk_refresh_token", "Refresh BroSDK auth material without echoing it.",
       kTokenSchema},
      [&client, &tasks](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string payload = PayloadFromParams(params, nullptr, 0);
        return AcceptedTaskResult(tasks, "brosdk_refresh_token",
                                  client.TokenUpdate(payload),
                                  "token update task accepted");
      });

  registry.Register(
      {"brosdk_netdiag",
       "Synchronously test network reachability through an optional proxy chain "
       "before opening browser environments.",
       kNetdiagSchema},
      [&client](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string url = json::GetString(params, "url");
        if (url.empty()) {
          return MakeToolError("INVALID_URL", "url is required");
        }
        if (IsBadUrl(url)) {
          return MakeToolError("URL_REJECTED", "unsafe URL scheme rejected");
        }
        const std::string payload = PayloadFromParams(params, nullptr, 0);
        SdkCallResult call = client.NetworkDiagnostics(payload);
        return {call.ok, SdkCallJson(call, call.ok, "netdiag complete")};
      });

  registry.Register(
      {"brosdk_env_getinfo",
       "Fetch full backend environment details for a single environment by envId.",
       kEnvGetInfoSchema},
      [&client](const rapidjson::Value &params) -> ToolResult {
        if (!params.IsObject()) {
          return MakeToolError("INVALID_PARAMS", "params must be an object");
        }
        const std::string env_id = json::GetString(params, "envId");
        if (env_id.empty()) {
          return MakeToolError("INVALID_ENVID", "envId is required");
        }
        const std::string payload = PayloadFromParams(params, nullptr, 0);
        SdkCallResult call = client.GetEnvInfo(payload);
        return {call.ok, SdkCallJson(call, call.ok, "env getinfo called")};
      });

  registry.Register(
      {"brosdk_get_task", "Read adapter task status by taskId.", kTaskSchema},
      [&tasks](const rapidjson::Value &params) -> ToolResult {
        const std::string task_id = json::GetString(params, "taskId");
        if (task_id.empty()) {
          return MakeToolError("INVALID_TASK_ID", "taskId is required");
        }
        auto task = tasks.Get(task_id);
        if (!task) {
          return MakeToolError("TASK_NOT_FOUND", "task not found");
        }
        return {true, tasks.ToJson(*task)};
      });

  registry.Register(
      {"brosdk_shutdown", "Stop the SDK singleton.", kEmptySchema},
      [&client](const rapidjson::Value &) -> ToolResult {
        SdkCallResult call = client.Shutdown();
        return {call.ok, SdkCallJson(call, call.ok, "sdk shutdown called")};
      });
}

} // namespace bromcp
