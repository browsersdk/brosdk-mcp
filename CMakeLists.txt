cmake_minimum_required(VERSION 3.16)

project(brosdk-mcp LANGUAGES C CXX)

set(xs3RDPARTY_DIR
    "${CMAKE_CURRENT_SOURCE_DIR}/third_party"
    CACHE PATH "Vendored third-party dependency root")
set(xsBIN_DIR
    "${CMAKE_BINARY_DIR}/bin"
    CACHE PATH "Runtime output root")

add_subdirectory(bromcp)
