#pragma once

#include <cstdint>
#include <string>

#include <rapidjson/document.h>

namespace bromcp::json {

bool Parse(const std::string &text, rapidjson::Document &doc,
           std::string *error = nullptr);

std::string Stringify(const rapidjson::Value &value);

std::string StringifyDocument(const rapidjson::Document &doc);

void AddString(rapidjson::Value &object, const char *key,
               const std::string &value,
               rapidjson::Document::AllocatorType &alloc);

void AddBool(rapidjson::Value &object, const char *key, bool value,
             rapidjson::Document::AllocatorType &alloc);

void AddInt(rapidjson::Value &object, const char *key, int32_t value,
            rapidjson::Document::AllocatorType &alloc);

void AddUInt64(rapidjson::Value &object, const char *key, uint64_t value,
               rapidjson::Document::AllocatorType &alloc);

bool HasMemberOfType(const rapidjson::Value &object, const char *key,
                     rapidjson::Type type);

std::string GetString(const rapidjson::Value &object, const char *key,
                      const std::string &fallback = {});

bool GetBool(const rapidjson::Value &object, const char *key, bool fallback);

std::string RedactJsonText(const std::string &text);

std::string BuildObjectWithoutKeys(const rapidjson::Value &object,
                                   const char *const *omitted_keys,
                                   size_t omitted_count);

} // namespace bromcp::json
