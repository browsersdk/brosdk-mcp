cmake_minimum_required(VERSION 3.16)

project(bromcp LANGUAGES C CXX)

set(BROMCP_REPO_ROOT "${CMAKE_CURRENT_SOURCE_DIR}/..")
if(NOT DEFINED xs3RDPARTY_DIR)
  set(xs3RDPARTY_DIR
      "${BROMCP_REPO_ROOT}/third_party"
      CACHE PATH "Vendored third-party dependency root")
endif()
if(NOT DEFINED xsBIN_DIR)
  set(xsBIN_DIR
      "${BROMCP_REPO_ROOT}/bin"
      CACHE PATH "Runtime output root")
endif()

set(BROSDK_INCLUDE_DIR
    "${BROMCP_REPO_ROOT}/brosdk/interface"
    CACHE PATH "BroSDK public header directory")
set(RAPIDJSON_INCLUDE_DIR
    "${xs3RDPARTY_DIR}/rapidjson/include"
    CACHE PATH "RapidJSON include directory")
set(WEBSOCKETPP_INCLUDE_DIR
    "${xs3RDPARTY_DIR}/websocketpp"
    CACHE PATH "websocketpp include directory")
set(ASIO_INCLUDE_DIR
    "${xs3RDPARTY_DIR}/asio/include"
    CACHE PATH "standalone Asio include directory")

set(BROMCP_HEADERS
    include/bromcp/brosdk_client.h
    include/bromcp/dlfcn_compat.h
    include/bromcp/json.h
    include/bromcp/mcp_server.h
    include/bromcp/task_store.h
    include/bromcp/tool_registry.h
    include/bromcp/tools.h)

set(BROMCP_SOURCES
    src/brosdk_client.cc
    src/json.cc
    src/main.cc
    src/mcp_server.cc
    src/task_store.cc
    src/tool_registry.cc
    src/tools.cc)

set(BROMCP_WIN32_DLFCN_INCLUDE_DIR "")
set(BROMCP_WIN32_DLFCN_SOURCE "")
set(BROMCP_WIN32_DLFCN_LIBRARY "")
set(BROMCP_WIN32_DLFCN_TARGET "")
if(WIN32)
  find_package(dlfcn-win32 CONFIG QUIET)
  if(TARGET dlfcn-win32::dl)
    set(BROMCP_WIN32_DLFCN_TARGET dlfcn-win32::dl)
  else()
    foreach(_dlfcn_dir
            "${xs3RDPARTY_DIR}/dlfcn-win32"
            "${xs3RDPARTY_DIR}/dlfcn"
            "${xs3RDPARTY_DIR}/win32-dlfcn")
      if(NOT EXISTS "${_dlfcn_dir}")
        continue()
      endif()

      if(EXISTS "${_dlfcn_dir}/include/dlfcn.h")
        set(BROMCP_WIN32_DLFCN_INCLUDE_DIR "${_dlfcn_dir}/include")
      elseif(EXISTS "${_dlfcn_dir}/dlfcn.h")
        set(BROMCP_WIN32_DLFCN_INCLUDE_DIR "${_dlfcn_dir}")
      endif()

      if(EXISTS "${_dlfcn_dir}/src/dlfcn.c")
        set(BROMCP_WIN32_DLFCN_SOURCE "${_dlfcn_dir}/src/dlfcn.c")
      elseif(EXISTS "${_dlfcn_dir}/dlfcn.c")
        set(BROMCP_WIN32_DLFCN_SOURCE "${_dlfcn_dir}/dlfcn.c")
      endif()

      find_library(
        BROMCP_WIN32_DLFCN_LIBRARY
        NAMES dlfcn dl
        PATHS "${_dlfcn_dir}/lib" "${_dlfcn_dir}/build"
        NO_DEFAULT_PATH)

      if(BROMCP_WIN32_DLFCN_INCLUDE_DIR
         AND (BROMCP_WIN32_DLFCN_SOURCE OR BROMCP_WIN32_DLFCN_LIBRARY))
        break()
      endif()
    endforeach()
  endif()

  if(BROMCP_WIN32_DLFCN_INCLUDE_DIR
     AND (BROMCP_WIN32_DLFCN_SOURCE OR BROMCP_WIN32_DLFCN_LIBRARY))
    list(APPEND BROMCP_SOURCES ${BROMCP_WIN32_DLFCN_SOURCE})
  endif()
endif()

add_executable(${PROJECT_NAME} ${BROMCP_HEADERS} ${BROMCP_SOURCES})
target_compile_features(${PROJECT_NAME} PRIVATE cxx_std_20)

target_include_directories(
  ${PROJECT_NAME}
  PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/include
          ${BROSDK_INCLUDE_DIR}
          ${RAPIDJSON_INCLUDE_DIR}
          ${WEBSOCKETPP_INCLUDE_DIR}
          ${ASIO_INCLUDE_DIR}
          ${BROMCP_WIN32_DLFCN_INCLUDE_DIR})

target_compile_definitions(${PROJECT_NAME} PRIVATE ASIO_STANDALONE
                                                   _WEBSOCKETPP_CPP11_STL_)

if(WIN32
   AND (BROMCP_WIN32_DLFCN_TARGET
        OR (BROMCP_WIN32_DLFCN_INCLUDE_DIR
            AND (BROMCP_WIN32_DLFCN_SOURCE OR BROMCP_WIN32_DLFCN_LIBRARY))))
  target_compile_definitions(${PROJECT_NAME} PRIVATE BROMCP_HAS_WIN32_DLFCN=1)
  if(BROMCP_WIN32_DLFCN_TARGET)
    target_link_libraries(${PROJECT_NAME}
                          PRIVATE ${BROMCP_WIN32_DLFCN_TARGET})
  elseif(BROMCP_WIN32_DLFCN_LIBRARY)
    target_link_libraries(${PROJECT_NAME}
                          PRIVATE ${BROMCP_WIN32_DLFCN_LIBRARY})
  endif()
endif()

if(WIN32)
  target_compile_definitions(${PROJECT_NAME} PRIVATE WIN32_LEAN_AND_MEAN
                                                     NOMINMAX)
  target_link_libraries(${PROJECT_NAME} PRIVATE ws2_32)
elseif(UNIX)
  target_link_libraries(${PROJECT_NAME} PRIVATE ${CMAKE_DL_LIBS} pthread)
endif()

if(MSVC)
  target_compile_options(${PROJECT_NAME} PRIVATE /W4 /permissive- /EHsc /utf-8
                                                 $<$<C_COMPILER_ID:MSVC>:/wd4996>)
else()
  target_compile_options(${PROJECT_NAME} PRIVATE -Wall -Wextra -Wpedantic
                                                 -finput-charset=UTF-8)
endif()

set(BROMCP_LOCAL_SOURCES ${BROMCP_HEADERS})
list(APPEND BROMCP_LOCAL_SOURCES
     src/brosdk_client.cc
     src/json.cc
     src/main.cc
     src/mcp_server.cc
     src/task_store.cc
     src/tool_registry.cc
     src/tools.cc)
source_group(TREE "${CMAKE_CURRENT_SOURCE_DIR}" FILES ${BROMCP_LOCAL_SOURCES})

set_target_properties(
  ${PROJECT_NAME}
  PROPERTIES RUNTIME_OUTPUT_DIRECTORY "${xsBIN_DIR}/$<CONFIG>"
             LIBRARY_OUTPUT_DIRECTORY "${xsBIN_DIR}/$<CONFIG>"
             ARCHIVE_OUTPUT_DIRECTORY "${xsBIN_DIR}/$<CONFIG>"
             FOLDER "projects")

if(TARGET brosdk)
  add_dependencies(${PROJECT_NAME} brosdk)
  add_custom_command(
    TARGET ${PROJECT_NAME}
    POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E copy_if_different $<TARGET_FILE:brosdk>
            $<TARGET_FILE_DIR:${PROJECT_NAME}>
    COMMENT "Copying brosdk runtime next to bromcp")
endif()
