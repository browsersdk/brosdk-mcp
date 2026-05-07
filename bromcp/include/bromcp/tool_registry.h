#pragma once

#include <functional>
#include <optional>
#include <string>
#include <unordered_map>
#include <vector>

#include <rapidjson/document.h>

namespace bromcp {

struct ToolResult {
  bool ok = false;
  std::string json;
};

struct ToolSpec {
  std::string name;
  std::string description;
  std::string input_schema_json;
};

class ToolRegistry {
public:
  using Handler = std::function<ToolResult(const rapidjson::Value &params)>;

  void Register(ToolSpec spec, Handler handler);

  ToolResult Call(const std::string &name, const rapidjson::Value &params) const;

  std::vector<ToolSpec> List() const;

  std::optional<ToolSpec> Find(const std::string &name) const;

  std::string McpToolsJson() const;

  std::string OpenAiToolsJson() const;

private:
  std::vector<ToolSpec> order_;
  std::unordered_map<std::string, Handler> handlers_;
};

ToolResult MakeToolError(const std::string &code, const std::string &message,
                         bool retryable = false);

ToolResult MakeToolJson(bool ok, const std::string &json);

} // namespace bromcp
