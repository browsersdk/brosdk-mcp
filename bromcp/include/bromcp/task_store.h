#pragma once

#include <cstdint>
#include <mutex>
#include <optional>
#include <string>
#include <unordered_map>
#include <vector>

namespace bromcp {

struct TaskRecord {
  std::string task_id;
  std::string source_req_id;
  std::string tool_name;
  std::string status;
  int progress = 0;
  std::string message;
  std::string result_json = "{}";
  std::string error_json = "null";
  std::string created_at;
  std::string updated_at;
};

class TaskStore {
public:
  TaskRecord CreateAccepted(const std::string &tool_name,
                            const std::string &source_req_id,
                            const std::string &message);

  std::optional<TaskRecord> Get(const std::string &task_id) const;

  std::vector<TaskRecord> List(size_t limit) const;

  void UpdateFromSdkEvent(int32_t code, const std::string &event_name,
                          const std::string &payload_json);

  std::string ToJson(const TaskRecord &task) const;

  std::string ListJson(size_t limit) const;

private:
  static std::string NowIso8601();
  static std::string ExtractReqId(const std::string &payload_json);
  static std::string ExtractType(const std::string &payload_json);
  static std::string ExtractMessage(const std::string &payload_json);
  static int ExtractProgress(const std::string &payload_json, int fallback);
  static std::string ToolNameForEvent(const std::string &event_name,
                                      const std::string &payload_json);
  static bool LooksFinalSuccess(const std::string &event_name,
                                const std::string &payload_json);
  static bool LooksFinalFailure(const std::string &event_name,
                                const std::string &payload_json);

  mutable std::mutex mutex_;
  uint64_t next_id_ = 1;
  std::vector<std::string> order_;
  std::unordered_map<std::string, TaskRecord> tasks_;
  std::unordered_map<std::string, std::string> req_to_task_;
};

} // namespace bromcp
