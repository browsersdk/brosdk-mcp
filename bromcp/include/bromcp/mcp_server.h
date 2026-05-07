#pragma once

#include <cstdint>
#include <string>

#include <rapidjson/document.h>

#include "bromcp/task_store.h"
#include "bromcp/tool_registry.h"

namespace bromcp {

struct ServerConfig {
  std::string host = "127.0.0.1";
  int port = 18766;
};

class McpServer {
public:
  McpServer(ToolRegistry &tools, TaskStore &tasks);

  bool Listen(const ServerConfig &config);
  int RunStdio();

private:
  std::string HandleJsonRpc(const std::string &body);
  std::string HandleJsonRpcObject(const rapidjson::Value &request);
  std::string JsonRpcError(const rapidjson::Value *id, int code,
                           const std::string &message) const;
  std::string JsonRpcResult(const rapidjson::Value *id,
                            const std::string &result_json) const;
  std::string ResourcesListJson() const;
  std::string ResourceReadJson(const std::string &uri) const;
  std::string PromptsListJson() const;
  std::string PromptGetJson(const std::string &name) const;
  std::string ToolCallResultJson(const ToolResult &tool_result) const;

  ToolRegistry &tools_;
  TaskStore &tasks_;
};

} // namespace bromcp
