#include "bromcp/json.h"

#include <algorithm>
#include <cctype>
#include <cstring>
#include <string_view>

#include <rapidjson/error/en.h>
#include <rapidjson/stringbuffer.h>
#include <rapidjson/writer.h>

namespace bromcp::json {
namespace {

bool IsSensitiveKey(std::string_view key) {
  std::string lowered(key);
  std::transform(lowered.begin(), lowered.end(), lowered.begin(),
                 [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
  return lowered.find("usersig") != std::string::npos ||
         lowered.find("token") != std::string::npos ||
         lowered.find("cookie") != std::string::npos ||
         lowered.find("storage") != std::string::npos ||
         lowered.find("password") != std::string::npos ||
         lowered.find("secret") != std::string::npos ||
         lowered.find("proxy") != std::string::npos;
}

void RedactValue(rapidjson::Value &value,
                 rapidjson::Document::AllocatorType &alloc) {
  if (value.IsObject()) {
    for (auto itr = value.MemberBegin(); itr != value.MemberEnd(); ++itr) {
      if (itr->name.IsString() &&
          IsSensitiveKey({itr->name.GetString(), itr->name.GetStringLength()})) {
        itr->value.SetString("[redacted]", alloc);
      } else {
        RedactValue(itr->value, alloc);
      }
    }
  } else if (value.IsArray()) {
    for (auto &item : value.GetArray()) {
      RedactValue(item, alloc);
    }
  }
}

bool KeyIsOmitted(const char *key, const char *const *omitted_keys,
                  size_t omitted_count) {
  for (size_t i = 0; i < omitted_count; ++i) {
    if (std::strcmp(key, omitted_keys[i]) == 0) {
      return true;
    }
  }
  return false;
}

} // namespace

bool Parse(const std::string &text, rapidjson::Document &doc,
           std::string *error) {
  doc.Parse(text.data(), static_cast<rapidjson::SizeType>(text.size()));
  if (!doc.HasParseError()) {
    return true;
  }

  if (error) {
    *error = std::string(rapidjson::GetParseError_En(doc.GetParseError())) +
             " at offset " + std::to_string(doc.GetErrorOffset());
  }
  return false;
}

std::string Stringify(const rapidjson::Value &value) {
  rapidjson::StringBuffer buffer;
  rapidjson::Writer<rapidjson::StringBuffer> writer(buffer);
  value.Accept(writer);
  return {buffer.GetString(), buffer.GetSize()};
}

std::string StringifyDocument(const rapidjson::Document &doc) {
  return Stringify(doc);
}

void AddString(rapidjson::Value &object, const char *key,
               const std::string &value,
               rapidjson::Document::AllocatorType &alloc) {
  object.AddMember(rapidjson::Value(key, alloc),
                   rapidjson::Value(value.c_str(),
                                    static_cast<rapidjson::SizeType>(
                                        value.size()),
                                    alloc),
                   alloc);
}

void AddBool(rapidjson::Value &object, const char *key, bool value,
             rapidjson::Document::AllocatorType &alloc) {
  object.AddMember(rapidjson::Value(key, alloc), rapidjson::Value(value),
                   alloc);
}

void AddInt(rapidjson::Value &object, const char *key, int32_t value,
            rapidjson::Document::AllocatorType &alloc) {
  object.AddMember(rapidjson::Value(key, alloc), rapidjson::Value(value),
                   alloc);
}

void AddUInt64(rapidjson::Value &object, const char *key, uint64_t value,
               rapidjson::Document::AllocatorType &alloc) {
  object.AddMember(rapidjson::Value(key, alloc), rapidjson::Value(value),
                   alloc);
}

bool HasMemberOfType(const rapidjson::Value &object, const char *key,
                     rapidjson::Type type) {
  return object.IsObject() && object.HasMember(key) && object[key].GetType() == type;
}

std::string GetString(const rapidjson::Value &object, const char *key,
                      const std::string &fallback) {
  if (!object.IsObject() || !object.HasMember(key) || !object[key].IsString()) {
    return fallback;
  }
  return {object[key].GetString(), object[key].GetStringLength()};
}

bool GetBool(const rapidjson::Value &object, const char *key, bool fallback) {
  if (!object.IsObject() || !object.HasMember(key) || !object[key].IsBool()) {
    return fallback;
  }
  return object[key].GetBool();
}

std::string RedactJsonText(const std::string &text) {
  rapidjson::Document doc;
  if (!Parse(text, doc)) {
    return text;
  }

  RedactValue(doc, doc.GetAllocator());
  return StringifyDocument(doc);
}

std::string BuildObjectWithoutKeys(const rapidjson::Value &object,
                                   const char *const *omitted_keys,
                                   size_t omitted_count) {
  rapidjson::Document doc(rapidjson::kObjectType);
  auto &alloc = doc.GetAllocator();
  if (!object.IsObject()) {
    return "{}";
  }

  for (auto itr = object.MemberBegin(); itr != object.MemberEnd(); ++itr) {
    if (!itr->name.IsString() ||
        KeyIsOmitted(itr->name.GetString(), omitted_keys, omitted_count)) {
      continue;
    }
    rapidjson::Value name;
    name.CopyFrom(itr->name, alloc);
    rapidjson::Value value;
    value.CopyFrom(itr->value, alloc);
    doc.AddMember(name, value, alloc);
  }

  return StringifyDocument(doc);
}

} // namespace bromcp::json
