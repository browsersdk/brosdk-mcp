#include "bromcp/tool_registry.h"

#include "bromcp/json.h"

namespace bromcp {
namespace {

void AddSchemaMember(rapidjson::Value &object, const char *key,
                     const std::string &schema_json,
                     rapidjson::Document::AllocatorType &alloc) {
  rapidjson::Document schema;
  if (json::Parse(schema_json, schema)) {
    rapidjson::Value copied;
    copied.CopyFrom(schema, alloc);
    object.AddMember(rapidjson::Value(key, alloc), copied, alloc);
  } else {
    object.AddMember(rapidjson::Value(key, alloc),
                     rapidjson::Value(rapidjson::kObjectType), alloc);
  }
}

} // namespace

void ToolRegistry::Register(ToolSpec spec, Handler handler) {
  handlers_[spec.name] = std::move(handler);
  order_.push_back(std::move(spec));
}

ToolResult ToolRegistry::Call(const std::string &name,
                              const rapidjson::Value &params) const {
  auto itr = handlers_.find(name);
  if (itr == handlers_.end()) {
    return MakeToolError("TOOL_NOT_FOUND", "unknown tool: " + name, false);
  }
  return itr->second(params);
}

std::vector<ToolSpec> ToolRegistry::List() const { return order_; }

std::optional<ToolSpec> ToolRegistry::Find(const std::string &name) const {
  for (const auto &tool : order_) {
    if (tool.name == name) {
      return tool;
    }
  }
  return std::nullopt;
}

std::string ToolRegistry::McpToolsJson() const {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  rapidjson::Value tools(rapidjson::kArrayType);

  for (const auto &tool : order_) {
    rapidjson::Value item(rapidjson::kObjectType);
    json::AddString(item, "name", tool.name, alloc);
    json::AddString(item, "description", tool.description, alloc);
    AddSchemaMember(item, "inputSchema", tool.input_schema_json, alloc);
    tools.PushBack(item, alloc);
  }

  doc.AddMember("tools", tools, alloc);
  return json::StringifyDocument(doc);
}

std::string ToolRegistry::OpenAiToolsJson() const {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  rapidjson::Value tools(rapidjson::kArrayType);

  for (const auto &tool : order_) {
    rapidjson::Value item(rapidjson::kObjectType);
    rapidjson::Value function(rapidjson::kObjectType);
    json::AddString(function, "name", tool.name, alloc);
    json::AddString(function, "description", tool.description, alloc);
    AddSchemaMember(function, "parameters", tool.input_schema_json, alloc);
    json::AddString(item, "type", "function", alloc);
    item.AddMember("function", function, alloc);
    tools.PushBack(item, alloc);
  }

  doc.AddMember("tools", tools, alloc);
  return json::StringifyDocument(doc);
}

ToolResult MakeToolError(const std::string &code, const std::string &message,
                         bool retryable) {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  json::AddBool(doc, "ok", false, alloc);
  rapidjson::Value error(rapidjson::kObjectType);
  json::AddString(error, "code", code, alloc);
  json::AddString(error, "message", message, alloc);
  json::AddBool(error, "retryable", retryable, alloc);
  doc.AddMember("error", error, alloc);
  return {false, json::StringifyDocument(doc)};
}

ToolResult MakeToolJson(bool ok, const std::string &text) {
  return {ok, text};
}

} // namespace bromcp
