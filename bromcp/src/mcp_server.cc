#include "bromcp/mcp_server.h"

#include <algorithm>
#include <cctype>
#include <condition_variable>
#include <deque>
#include <functional>
#include <iostream>
#include <mutex>
#include <sstream>
#include <string_view>
#include <thread>
#include <utility>
#include <vector>

#include <websocketpp/config/asio_no_tls.hpp>
#include <websocketpp/server.hpp>

#include "bromcp/json.h"

namespace bromcp {
namespace {

using WebSocketServer = websocketpp::server<websocketpp::config::asio>;
using HttpStatus = websocketpp::http::status_code::value;

class WorkQueue {
public:
  explicit WorkQueue(size_t worker_count) {
    worker_count = std::max<size_t>(1, worker_count);
    workers_.reserve(worker_count);
    for (size_t i = 0; i < worker_count; ++i) {
      workers_.emplace_back([this]() { Run(); });
    }
  }

  ~WorkQueue() {
    {
      std::lock_guard<std::mutex> lock(mutex_);
      stopped_ = true;
    }
    cv_.notify_all();
    for (std::thread &worker : workers_) {
      if (worker.joinable()) {
        worker.join();
      }
    }
  }

  void Submit(std::function<void()> work) {
    {
      std::lock_guard<std::mutex> lock(mutex_);
      if (stopped_) {
        return;
      }
      queue_.push_back(std::move(work));
    }
    cv_.notify_one();
  }

private:
  void Run() {
    for (;;) {
      std::function<void()> work;
      {
        std::unique_lock<std::mutex> lock(mutex_);
        cv_.wait(lock, [this]() { return stopped_ || !queue_.empty(); });
        if (stopped_ && queue_.empty()) {
          return;
        }
        work = std::move(queue_.front());
        queue_.pop_front();
      }
      work();
    }
  }

  std::mutex mutex_;
  std::condition_variable cv_;
  bool stopped_ = false;
  std::deque<std::function<void()>> queue_;
  std::vector<std::thread> workers_;
};

void SetJson(const WebSocketServer::connection_ptr &con,
             const std::string &body,
             HttpStatus status = websocketpp::http::status_code::ok) {
  con->set_status(status);
  con->replace_header("Access-Control-Allow-Origin", "http://127.0.0.1");
  con->replace_header("Access-Control-Allow-Headers", "content-type");
  con->replace_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS");
  con->replace_header("Content-Type", "application/json; charset=utf-8");
  con->set_body(body);
}

std::string PathOnly(const std::string &uri) {
  const size_t query = uri.find('?');
  return query == std::string::npos ? uri : uri.substr(0, query);
}

bool StartsWith(std::string_view value, std::string_view prefix) {
  return value.size() >= prefix.size() &&
         value.compare(0, prefix.size(), prefix) == 0;
}

bool IsSafeIdentifier(std::string_view value) {
  return !value.empty() &&
         std::all_of(value.begin(), value.end(), [](unsigned char c) {
           return std::isalnum(c) || c == '_' || c == '-';
         });
}

std::string ErrorJson(const std::string &code, const std::string &message) {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  json::AddBool(doc, "ok", false, alloc);
  rapidjson::Value error(rapidjson::kObjectType);
  json::AddString(error, "code", code, alloc);
  json::AddString(error, "message", message, alloc);
  doc.AddMember("error", error, alloc);
  return json::StringifyDocument(doc);
}

void SendWebSocketText(WebSocketServer &server, websocketpp::connection_hdl hdl,
                       std::string body) {
  server.get_io_service().post(
      [&server, hdl, body = std::move(body)]() mutable {
        websocketpp::lib::error_code ec;
        server.send(hdl, body, websocketpp::frame::opcode::text, ec);
      });
}

rapidjson::Value CopyId(const rapidjson::Value *id,
                        rapidjson::Document::AllocatorType &alloc) {
  rapidjson::Value copied;
  if (id) {
    copied.CopyFrom(*id, alloc);
  } else {
    copied.SetNull();
  }
  return copied;
}

} // namespace

McpServer::McpServer(ToolRegistry &tools, TaskStore &tasks)
    : tools_(tools), tasks_(tasks) {}

bool McpServer::Listen(const ServerConfig &config) {
  WebSocketServer server;
  WorkQueue workers(1);

  try {
    server.clear_access_channels(websocketpp::log::alevel::all);
    server.clear_error_channels(websocketpp::log::elevel::all);
    server.init_asio();
    server.set_reuse_addr(true);

    server.set_http_handler([this, &server](websocketpp::connection_hdl hdl) {
      websocketpp::lib::error_code ec;
      auto con = server.get_con_from_hdl(hdl, ec);
      if (ec || !con) {
        return;
      }

      const auto &request = con->get_request();
      const std::string method = request.get_method();
      const std::string path = PathOnly(request.get_uri());

      try {
        if (method == "OPTIONS") {
          SetJson(con, R"({"ok":true})");
          return;
        }

        if (method == "GET" && (path == "/" || path == "/health")) {
          rapidjson::Document doc(rapidjson::kObjectType);
          auto &alloc = doc.GetAllocator();
          json::AddBool(doc, "ok", true, alloc);
          json::AddString(doc, "server", "bromcp", alloc);
          json::AddString(doc, "transport", "websocket-jsonrpc", alloc);
          json::AddString(doc, "websocketPath", "/mcp", alloc);
          json::AddUInt64(doc, "toolCount", tools_.List().size(), alloc);
          SetJson(con, json::StringifyDocument(doc));
          return;
        }

        if (method == "GET" && path == "/tools") {
          SetJson(con, tools_.OpenAiToolsJson());
          return;
        }

        if (method == "GET" && path == "/mcp/tools") {
          SetJson(con, tools_.McpToolsJson());
          return;
        }

        if (method == "GET" && path == "/tasks") {
          SetJson(con, tasks_.ListJson(50));
          return;
        }

        if (method == "GET" && StartsWith(path, "/tasks/")) {
          const std::string task_id = path.substr(std::string("/tasks/").size());
          if (!IsSafeIdentifier(task_id)) {
            SetJson(con, ErrorJson("INVALID_TASK_ID", "invalid task id"),
                    websocketpp::http::status_code::bad_request);
            return;
          }
          auto task = tasks_.Get(task_id);
          if (!task) {
            SetJson(con, ErrorJson("TASK_NOT_FOUND", "task not found"),
                    websocketpp::http::status_code::not_found);
            return;
          }
          SetJson(con, tasks_.ToJson(*task));
          return;
        }

        if (method == "POST" && path == "/mcp") {
          const std::string response = HandleJsonRpc(con->get_request_body());
          SetJson(con, response.empty() ? "{}" : response);
          return;
        }

        if (method == "POST" && StartsWith(path, "/tools/")) {
          const std::string name = path.substr(std::string("/tools/").size());
          if (!IsSafeIdentifier(name)) {
            SetJson(con, ErrorJson("INVALID_TOOL_NAME", "invalid tool name"),
                    websocketpp::http::status_code::bad_request);
            return;
          }
          rapidjson::Document params;
          std::string parse_error;
          if (!json::Parse(con->get_request_body(), params, &parse_error)) {
            ToolResult error =
                MakeToolError("INVALID_JSON", "invalid JSON: " + parse_error);
            SetJson(con, error.json,
                    websocketpp::http::status_code::bad_request);
            return;
          }
          ToolResult result = tools_.Call(name, params);
          SetJson(con, result.json,
                  result.ok ? websocketpp::http::status_code::ok
                            : websocketpp::http::status_code::bad_request);
          return;
        }

        SetJson(con, ErrorJson("NOT_FOUND", "route not found"),
                websocketpp::http::status_code::not_found);
      } catch (const std::exception &ex) {
        SetJson(con, ErrorJson("INTERNAL_ERROR", ex.what()),
                websocketpp::http::status_code::internal_server_error);
      }
    });

    server.set_message_handler(
        [this, &server, &workers](websocketpp::connection_hdl hdl,
                                  WebSocketServer::message_ptr message) {
          std::string payload = message->get_payload();
          workers.Submit(
              [this, &server, hdl, payload = std::move(payload)]() mutable {
                std::string response;
                try {
                  response = HandleJsonRpc(payload);
                } catch (const std::exception &ex) {
                  response = JsonRpcError(nullptr, -32603, ex.what());
                }
                if (!response.empty()) {
                  SendWebSocketText(server, hdl, std::move(response));
                }
              });
        });

    websocketpp::lib::error_code ec;
    server.listen(config.host, std::to_string(config.port), ec);
    if (ec) {
      std::cerr << "failed to listen on " << config.host << ":" << config.port
                << ": " << ec.message() << "\n";
      return false;
    }
    server.start_accept();

    std::cout << "bromcp listening on ws://" << config.host << ":"
              << config.port << "/mcp\n";
    std::cout << "JSON-RPC endpoint: WebSocket /mcp\n";
    std::cout << "OpenAI tool metadata: GET /tools\n";
    server.run();
    return true;
  } catch (const std::exception &ex) {
    std::cerr << "bromcp websocket server failed: " << ex.what() << "\n";
    return false;
  }
}

int McpServer::RunStdio() {
  std::cin.sync_with_stdio(false);
  std::cout.sync_with_stdio(false);

  while (std::cin.good()) {
    std::string line;
    size_t content_length = 0;

    while (std::getline(std::cin, line)) {
      if (!line.empty() && line.back() == '\r') {
        line.pop_back();
      }
      if (line.empty()) {
        break;
      }
      const std::string prefix = "Content-Length:";
      if (line.size() >= prefix.size()) {
        bool matches = true;
        for (size_t i = 0; i < prefix.size(); ++i) {
          if (std::tolower(static_cast<unsigned char>(line[i])) !=
              std::tolower(static_cast<unsigned char>(prefix[i]))) {
            matches = false;
            break;
          }
        }
        if (matches) {
          std::string value = line.substr(prefix.size());
          content_length = static_cast<size_t>(std::stoull(value));
        }
      }
    }

    if (content_length == 0) {
      if (std::cin.eof()) {
        break;
      }
      continue;
    }

    std::string body(content_length, '\0');
    std::cin.read(body.data(), static_cast<std::streamsize>(content_length));
    if (static_cast<size_t>(std::cin.gcount()) != content_length) {
      break;
    }

    const std::string response = HandleJsonRpc(body);
    if (response.empty()) {
      continue;
    }
    std::cout << "Content-Length: " << response.size()
              << "\r\n\r\n"
              << response;
    std::cout.flush();
  }

  return 0;
}

std::string McpServer::HandleJsonRpc(const std::string &body) {
  rapidjson::Document request;
  std::string error;
  if (!json::Parse(body, request, &error)) {
    return JsonRpcError(nullptr, -32700, "parse error: " + error);
  }

  if (request.IsArray()) {
    rapidjson::Document doc(rapidjson::kArrayType);
    auto &alloc = doc.GetAllocator();
    for (const auto &item : request.GetArray()) {
      rapidjson::Document response;
      if (json::Parse(HandleJsonRpcObject(item), response)) {
        rapidjson::Value copied;
        copied.CopyFrom(response, alloc);
        doc.PushBack(copied, alloc);
      }
    }
    return json::StringifyDocument(doc);
  }

  return HandleJsonRpcObject(request);
}

std::string McpServer::HandleJsonRpcObject(const rapidjson::Value &request) {
  if (!request.IsObject()) {
    return JsonRpcError(nullptr, -32600, "request must be an object");
  }

  const rapidjson::Value *id = request.HasMember("id") ? &request["id"] : nullptr;
  if (!request.HasMember("method") || !request["method"].IsString()) {
    return JsonRpcError(id, -32600, "method is required");
  }

  const std::string method{request["method"].GetString(),
                           request["method"].GetStringLength()};

  if (method == "initialize") {
    rapidjson::Document doc(rapidjson::kObjectType);
    auto &alloc = doc.GetAllocator();
    json::AddString(doc, "protocolVersion", "2024-11-05", alloc);
    rapidjson::Value capabilities(rapidjson::kObjectType);
    capabilities.AddMember("tools", rapidjson::Value(rapidjson::kObjectType),
                           alloc);
    capabilities.AddMember("resources",
                           rapidjson::Value(rapidjson::kObjectType), alloc);
    capabilities.AddMember("prompts", rapidjson::Value(rapidjson::kObjectType),
                           alloc);
    doc.AddMember("capabilities", capabilities, alloc);
    rapidjson::Value server_info(rapidjson::kObjectType);
    json::AddString(server_info, "name", "bromcp", alloc);
    json::AddString(server_info, "version", "0.1.0", alloc);
    doc.AddMember("serverInfo", server_info, alloc);
    return JsonRpcResult(id, json::StringifyDocument(doc));
  }

  if (method == "tools/list") {
    return JsonRpcResult(id, tools_.McpToolsJson());
  }

  if (method == "tools/call") {
    if (!request.HasMember("params") || !request["params"].IsObject()) {
      return JsonRpcError(id, -32602, "tools/call params must be an object");
    }
    const auto &params = request["params"];
    if (!params.HasMember("name") || !params["name"].IsString()) {
      return JsonRpcError(id, -32602, "tools/call requires params.name");
    }
    rapidjson::Value empty_arguments(rapidjson::kObjectType);
    const rapidjson::Value &arguments =
        params.HasMember("arguments") ? params["arguments"] : empty_arguments;
    const std::string name{params["name"].GetString(),
                           params["name"].GetStringLength()};
    ToolResult tool_result = tools_.Call(name, arguments);
    return JsonRpcResult(id, ToolCallResultJson(tool_result));
  }

  if (method == "resources/list") {
    return JsonRpcResult(id, ResourcesListJson());
  }

  if (method == "resources/read") {
    if (!request.HasMember("params") || !request["params"].IsObject() ||
        !request["params"].HasMember("uri") ||
        !request["params"]["uri"].IsString()) {
      return JsonRpcError(id, -32602, "resources/read requires params.uri");
    }
    const auto &uri = request["params"]["uri"];
    return JsonRpcResult(
        id, ResourceReadJson({uri.GetString(), uri.GetStringLength()}));
  }

  if (method == "prompts/list") {
    return JsonRpcResult(id, PromptsListJson());
  }

  if (method == "prompts/get") {
    if (!request.HasMember("params") || !request["params"].IsObject() ||
        !request["params"].HasMember("name") ||
        !request["params"]["name"].IsString()) {
      return JsonRpcError(id, -32602, "prompts/get requires params.name");
    }
    const auto &name = request["params"]["name"];
    return JsonRpcResult(
        id, PromptGetJson({name.GetString(), name.GetStringLength()}));
  }

  if (method == "ping") {
    return JsonRpcResult(id, "{}");
  }

  if (method == "notifications/initialized") {
    return id ? JsonRpcResult(id, R"({"ok":true})") : std::string();
  }

  return JsonRpcError(id, -32601, "method not found: " + method);
}

std::string McpServer::JsonRpcError(const rapidjson::Value *id, int code,
                                    const std::string &message) const {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  json::AddString(doc, "jsonrpc", "2.0", alloc);
  doc.AddMember("id", CopyId(id, alloc), alloc);
  rapidjson::Value error(rapidjson::kObjectType);
  json::AddInt(error, "code", code, alloc);
  json::AddString(error, "message", message, alloc);
  doc.AddMember("error", error, alloc);
  return json::StringifyDocument(doc);
}

std::string McpServer::JsonRpcResult(const rapidjson::Value *id,
                                     const std::string &result_json) const {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  json::AddString(doc, "jsonrpc", "2.0", alloc);
  doc.AddMember("id", CopyId(id, alloc), alloc);

  rapidjson::Document result_doc;
  if (json::Parse(result_json, result_doc)) {
    rapidjson::Value copied;
    copied.CopyFrom(result_doc, alloc);
    doc.AddMember("result", copied, alloc);
  } else {
    json::AddString(doc, "result", result_json, alloc);
  }
  return json::StringifyDocument(doc);
}

std::string McpServer::ResourcesListJson() const {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  rapidjson::Value resources(rapidjson::kArrayType);

  auto add = [&](const char *uri, const char *name, const char *description) {
    rapidjson::Value item(rapidjson::kObjectType);
    json::AddString(item, "uri", uri, alloc);
    json::AddString(item, "name", name, alloc);
    json::AddString(item, "description", description, alloc);
    json::AddString(item, "mimeType", "application/json", alloc);
    resources.PushBack(item, alloc);
  };

  add("bromcp://tools", "BroSDK MCP tools", "Registered tool metadata");
  add("bromcp://tasks", "BroSDK tasks", "Recent async task status");
  doc.AddMember("resources", resources, alloc);
  return json::StringifyDocument(doc);
}

std::string McpServer::ResourceReadJson(const std::string &uri) const {
  std::string text;
  if (uri == "bromcp://tools") {
    text = tools_.McpToolsJson();
  } else if (uri == "bromcp://tasks") {
    text = tasks_.ListJson(50);
  } else {
    return R"({"contents":[]})";
  }

  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  rapidjson::Value contents(rapidjson::kArrayType);
  rapidjson::Value item(rapidjson::kObjectType);
  json::AddString(item, "uri", uri, alloc);
  json::AddString(item, "mimeType", "application/json", alloc);
  json::AddString(item, "text", text, alloc);
  contents.PushBack(item, alloc);
  doc.AddMember("contents", contents, alloc);
  return json::StringifyDocument(doc);
}

std::string McpServer::PromptsListJson() const {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  rapidjson::Value prompts(rapidjson::kArrayType);

  auto add = [&](const char *name, const char *description) {
    rapidjson::Value item(rapidjson::kObjectType);
    json::AddString(item, "name", name, alloc);
    json::AddString(item, "description", description, alloc);
    prompts.PushBack(item, alloc);
  };

  add("brosdk_open_env", "Open a BroSDK browser env and poll the task result");
  add("brosdk_close_env", "Close BroSDK browser envs and verify completion");
  add("brosdk_health", "Inspect BroSDK health without exposing secrets");
  doc.AddMember("prompts", prompts, alloc);
  return json::StringifyDocument(doc);
}

std::string McpServer::PromptGetJson(const std::string &name) const {
  std::string text;
  if (name == "brosdk_open_env") {
    text = "Use brosdk_health_check first. Then call "
           "brosdk_open_browser_envs with string envId values and safe urls. "
           "Poll brosdk_get_task until status is succeeded or failed.";
  } else if (name == "brosdk_close_env") {
    text = "Call brosdk_close_browser_envs with string env ids. Poll "
           "brosdk_get_task and confirm browser-close-success before reporting "
           "completion.";
  } else if (name == "brosdk_health") {
    text = "Call brosdk_health_check. Do not print userSig, proxy credentials, "
           "cookies, storage, or local paths unless explicitly needed.";
  } else {
    text = "Unknown prompt.";
  }

  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  json::AddString(doc, "description", name, alloc);
  rapidjson::Value messages(rapidjson::kArrayType);
  rapidjson::Value message(rapidjson::kObjectType);
  json::AddString(message, "role", "user", alloc);
  rapidjson::Value content(rapidjson::kObjectType);
  json::AddString(content, "type", "text", alloc);
  json::AddString(content, "text", text, alloc);
  message.AddMember("content", content, alloc);
  messages.PushBack(message, alloc);
  doc.AddMember("messages", messages, alloc);
  return json::StringifyDocument(doc);
}

std::string McpServer::ToolCallResultJson(const ToolResult &tool_result) const {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  rapidjson::Value content(rapidjson::kArrayType);
  rapidjson::Value text(rapidjson::kObjectType);
  json::AddString(text, "type", "text", alloc);
  json::AddString(text, "text", tool_result.json, alloc);
  content.PushBack(text, alloc);
  doc.AddMember("content", content, alloc);
  json::AddBool(doc, "isError", !tool_result.ok, alloc);

  rapidjson::Document structured;
  if (json::Parse(tool_result.json, structured)) {
    rapidjson::Value copied;
    copied.CopyFrom(structured, alloc);
    doc.AddMember("structuredContent", copied, alloc);
  }

  return json::StringifyDocument(doc);
}

} // namespace bromcp
