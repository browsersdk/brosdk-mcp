#include "bromcp/task_store.h"

#include <algorithm>
#include <chrono>
#include <cctype>
#include <iomanip>
#include <sstream>

#include "bromcp/json.h"

namespace bromcp {
namespace {

std::string Lower(std::string value) {
  std::transform(value.begin(), value.end(), value.begin(),
                 [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
  return value;
}

} // namespace

TaskRecord TaskStore::CreateAccepted(const std::string &tool_name,
                                     const std::string &source_req_id,
                                     const std::string &message) {
  std::lock_guard<std::mutex> lock(mutex_);

  const std::string now = NowIso8601();
  TaskRecord task;
  task.task_id = "task_" + std::to_string(next_id_++);
  task.source_req_id = source_req_id;
  task.tool_name = tool_name;
  task.status = "running";
  task.progress = 0;
  task.message = message;
  task.created_at = now;
  task.updated_at = now;

  order_.push_back(task.task_id);
  tasks_[task.task_id] = task;
  if (!task.source_req_id.empty()) {
    req_to_task_[task.source_req_id] = task.task_id;
  }
  return task;
}

std::optional<TaskRecord> TaskStore::Get(const std::string &task_id) const {
  std::lock_guard<std::mutex> lock(mutex_);
  auto itr = tasks_.find(task_id);
  if (itr == tasks_.end()) {
    return std::nullopt;
  }
  return itr->second;
}

std::vector<TaskRecord> TaskStore::List(size_t limit) const {
  std::lock_guard<std::mutex> lock(mutex_);

  std::vector<TaskRecord> result;
  const size_t count = std::min(limit, order_.size());
  result.reserve(count);

  for (size_t i = 0; i < count; ++i) {
    const std::string &task_id = order_[order_.size() - 1 - i];
    auto itr = tasks_.find(task_id);
    if (itr != tasks_.end()) {
      result.push_back(itr->second);
    }
  }
  return result;
}

void TaskStore::UpdateFromSdkEvent(int32_t, const std::string &event_name,
                                   const std::string &payload_json) {
  const std::string req_id = ExtractReqId(payload_json);

  std::lock_guard<std::mutex> lock(mutex_);
  std::string task_id;
  if (!req_id.empty()) {
    auto req_itr = req_to_task_.find(req_id);
    if (req_itr != req_to_task_.end()) {
      task_id = req_itr->second;
    }
  }

  if (task_id.empty()) {
    const std::string tool_name = ToolNameForEvent(event_name, payload_json);
    if (tool_name.empty()) {
      return;
    }
    for (auto itr = order_.rbegin(); itr != order_.rend(); ++itr) {
      auto task_itr = tasks_.find(*itr);
      if (task_itr == tasks_.end()) {
        continue;
      }
      const TaskRecord &candidate = task_itr->second;
      if (candidate.tool_name == tool_name && candidate.status == "running" &&
          candidate.source_req_id.empty()) {
        task_id = candidate.task_id;
        break;
      }
    }
    if (task_id.empty()) {
      return;
    }
    if (!req_id.empty()) {
      req_to_task_[req_id] = task_id;
    }
  }

  auto task_itr = tasks_.find(task_id);
  if (task_itr == tasks_.end()) {
    return;
  }

  TaskRecord &task = task_itr->second;
  task.updated_at = NowIso8601();
  if (task.source_req_id.empty() && !req_id.empty()) {
    task.source_req_id = req_id;
  }
  task.message = ExtractMessage(payload_json);
  if (task.message.empty()) {
    task.message = ExtractType(payload_json);
  }
  if (task.message.empty()) {
    task.message = event_name;
  }
  task.result_json = json::RedactJsonText(payload_json);
  task.progress = ExtractProgress(payload_json, task.progress);

  if (LooksFinalSuccess(event_name, payload_json)) {
    task.status = "succeeded";
    task.progress = 100;
    task.error_json = "null";
  } else if (LooksFinalFailure(event_name, payload_json)) {
    task.status = "failed";
    task.progress = 100;
    task.error_json = task.result_json;
  } else {
    task.status = "running";
  }
}

std::string TaskStore::ToJson(const TaskRecord &task) const {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();

  json::AddString(doc, "taskId", task.task_id, alloc);
  json::AddString(doc, "sourceReqId", task.source_req_id, alloc);
  json::AddString(doc, "toolName", task.tool_name, alloc);
  json::AddString(doc, "status", task.status, alloc);
  json::AddInt(doc, "progress", task.progress, alloc);
  json::AddString(doc, "message", task.message, alloc);

  rapidjson::Document result_doc;
  if (json::Parse(task.result_json, result_doc)) {
    rapidjson::Value copied;
    copied.CopyFrom(result_doc, alloc);
    doc.AddMember("result", copied, alloc);
  } else {
    json::AddString(doc, "result", task.result_json, alloc);
  }

  rapidjson::Document error_doc;
  if (json::Parse(task.error_json, error_doc)) {
    rapidjson::Value copied;
    copied.CopyFrom(error_doc, alloc);
    doc.AddMember("error", copied, alloc);
  } else {
    json::AddString(doc, "error", task.error_json, alloc);
  }

  json::AddString(doc, "createdAt", task.created_at, alloc);
  json::AddString(doc, "updatedAt", task.updated_at, alloc);

  return json::StringifyDocument(doc);
}

std::string TaskStore::ListJson(size_t limit) const {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  rapidjson::Value items(rapidjson::kArrayType);

  for (const auto &task : List(limit)) {
    rapidjson::Document task_doc;
    if (json::Parse(ToJson(task), task_doc)) {
      rapidjson::Value copied;
      copied.CopyFrom(task_doc, alloc);
      items.PushBack(copied, alloc);
    }
  }

  doc.AddMember("tasks", items, alloc);
  return json::StringifyDocument(doc);
}

std::string TaskStore::NowIso8601() {
  const auto now = std::chrono::system_clock::now();
  const std::time_t time = std::chrono::system_clock::to_time_t(now);
  std::tm tm{};
#if defined(_WIN32)
  gmtime_s(&tm, &time);
#else
  gmtime_r(&time, &tm);
#endif

  std::ostringstream oss;
  oss << std::put_time(&tm, "%Y-%m-%dT%H:%M:%SZ");
  return oss.str();
}

std::string TaskStore::ExtractReqId(const std::string &payload_json) {
  rapidjson::Document doc;
  if (!json::Parse(payload_json, doc) || !doc.IsObject()) {
    return {};
  }

  if (doc.HasMember("reqId")) {
    const auto &req = doc["reqId"];
    if (req.IsString()) {
      return {req.GetString(), req.GetStringLength()};
    }
    if (req.IsInt64()) {
      return std::to_string(req.GetInt64());
    }
    if (req.IsUint64()) {
      return std::to_string(req.GetUint64());
    }
  }

  return {};
}

std::string TaskStore::ExtractType(const std::string &payload_json) {
  rapidjson::Document doc;
  if (!json::Parse(payload_json, doc) || !doc.IsObject()) {
    return {};
  }
  if (doc.HasMember("type") && doc["type"].IsString()) {
    return {doc["type"].GetString(), doc["type"].GetStringLength()};
  }
  return {};
}

std::string TaskStore::ExtractMessage(const std::string &payload_json) {
  rapidjson::Document doc;
  if (!json::Parse(payload_json, doc) || !doc.IsObject()) {
    return {};
  }

  if (doc.HasMember("message") && doc["message"].IsString()) {
    return {doc["message"].GetString(), doc["message"].GetStringLength()};
  }
  if (doc.HasMember("msg") && doc["msg"].IsString()) {
    return {doc["msg"].GetString(), doc["msg"].GetStringLength()};
  }
  if (doc.HasMember("data") && doc["data"].IsObject()) {
    const auto &data = doc["data"];
    if (data.HasMember("message") && data["message"].IsString()) {
      return {data["message"].GetString(), data["message"].GetStringLength()};
    }
    if (data.HasMember("msg") && data["msg"].IsString()) {
      return {data["msg"].GetString(), data["msg"].GetStringLength()};
    }
  }
  return {};
}

int TaskStore::ExtractProgress(const std::string &payload_json, int fallback) {
  rapidjson::Document doc;
  if (!json::Parse(payload_json, doc) || !doc.IsObject()) {
    return fallback;
  }
  const auto read_progress = [](const rapidjson::Value &value,
                                int fallback_value) {
    if (value.IsInt()) {
      return std::clamp(value.GetInt(), 0, 100);
    }
    if (value.IsUint()) {
      return std::clamp(static_cast<int>(value.GetUint()), 0, 100);
    }
    return fallback_value;
  };
  if (doc.HasMember("progress")) {
    return read_progress(doc["progress"], fallback);
  }
  if (doc.HasMember("data") && doc["data"].IsObject() &&
      doc["data"].HasMember("progress")) {
    return read_progress(doc["data"]["progress"], fallback);
  }
  return fallback;
}

std::string TaskStore::ToolNameForEvent(const std::string &event_name,
                                        const std::string &payload_json) {
  const std::string event = Lower(event_name + " " + ExtractType(payload_json) +
                                  " " + payload_json);
  if (event.find("browser-install") != std::string::npos) {
    return "brosdk_browser_install";
  }
  if (event.find("browser-open") != std::string::npos) {
    return "brosdk_open_browser_envs";
  }
  if (event.find("browser-close") != std::string::npos) {
    return "brosdk_close_browser_envs";
  }
  if (event.find("token") != std::string::npos) {
    return "brosdk_refresh_token";
  }
  if (event.find("sdk-init") != std::string::npos) {
    return "brosdk_init";
  }
  return {};
}

bool TaskStore::LooksFinalSuccess(const std::string &event_name,
                                  const std::string &payload_json) {
  const std::string event = Lower(event_name + " " + payload_json);
  return event.find("success") != std::string::npos ||
         event.find("succeeded") != std::string::npos;
}

bool TaskStore::LooksFinalFailure(const std::string &event_name,
                                  const std::string &payload_json) {
  const std::string event = Lower(event_name + " " + payload_json);
  return event.find("failed") != std::string::npos ||
         event.find("failure") != std::string::npos ||
         event.find("timeout") != std::string::npos ||
         event.find("error") != std::string::npos;
}

} // namespace bromcp
