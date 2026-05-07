#pragma once

#if defined(_WIN32) && !defined(BROMCP_HAS_WIN32_DLFCN)

#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#ifndef NOMINMAX
#define NOMINMAX
#endif
#include <windows.h>

#include <string>

#ifndef RTLD_NOW
#define RTLD_NOW 2
#endif

#ifndef RTLD_LOCAL
#define RTLD_LOCAL 0
#endif

inline std::string &bromcp_dlerror_storage() {
  thread_local std::string error;
  return error;
}

inline void bromcp_set_dlerror_from_windows(const char *prefix) {
  const DWORD error = GetLastError();
  if (error == 0) {
    bromcp_dlerror_storage().clear();
    return;
  }

  LPSTR buffer = nullptr;
  const DWORD size = FormatMessageA(
      FORMAT_MESSAGE_ALLOCATE_BUFFER | FORMAT_MESSAGE_FROM_SYSTEM |
          FORMAT_MESSAGE_IGNORE_INSERTS,
      nullptr, error, MAKELANGID(LANG_NEUTRAL, SUBLANG_DEFAULT),
      reinterpret_cast<LPSTR>(&buffer), 0, nullptr);
  std::string message =
      size && buffer ? std::string(buffer, size)
                     : ("Windows error " + std::to_string(error));
  if (buffer) {
    LocalFree(buffer);
  }
  bromcp_dlerror_storage() = std::string(prefix) + ": " + message;
}

inline void *dlopen(const char *filename, int) {
  HMODULE module = LoadLibraryA(filename);
  if (!module) {
    bromcp_set_dlerror_from_windows("dlopen");
    return nullptr;
  }
  bromcp_dlerror_storage().clear();
  return reinterpret_cast<void *>(module);
}

inline void *dlsym(void *handle, const char *symbol) {
  FARPROC proc = GetProcAddress(reinterpret_cast<HMODULE>(handle), symbol);
  if (!proc) {
    bromcp_set_dlerror_from_windows("dlsym");
    return nullptr;
  }
  bromcp_dlerror_storage().clear();
  return reinterpret_cast<void *>(proc);
}

inline int dlclose(void *handle) {
  if (!FreeLibrary(reinterpret_cast<HMODULE>(handle))) {
    bromcp_set_dlerror_from_windows("dlclose");
    return -1;
  }
  bromcp_dlerror_storage().clear();
  return 0;
}

inline char *dlerror() {
  std::string &error = bromcp_dlerror_storage();
  return error.empty() ? nullptr : error.data();
}

#else

#include <dlfcn.h>

#endif
