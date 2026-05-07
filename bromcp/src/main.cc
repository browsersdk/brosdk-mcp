#include <cstdlib>
#include <iostream>
#include <string>

#include "bromcp/brosdk_client.h"
#include "bromcp/mcp_server.h"
#include "bromcp/task_store.h"
#include "bromcp/tools.h"

namespace {

void PrintUsage() {
  std::cout
      << "Usage: bromcp [--stdio] [--host 127.0.0.1] [--port 18766] "
         "[--brosdk path/to/brosdk.dll]\n\n"
      << "Default mode starts a WebSocket JSON-RPC server at /mcp.\n\n"
      << "Environment:\n"
      << "  BROSDK_LIBRARY   default BroSDK dynamic library path\n";
}

std::string ReadArg(int argc, char **argv, int &index) {
  if (index + 1 >= argc) {
    return {};
  }
  ++index;
  return argv[index];
}

} // namespace

int main(int argc, char **argv) {
  bromcp::ServerConfig config;
  std::string brosdk_library;
  bool stdio = false;

  for (int i = 1; i < argc; ++i) {
    const std::string arg = argv[i];
    if (arg == "--help" || arg == "-h") {
      PrintUsage();
      return 0;
    }
    if (arg == "--stdio") {
      stdio = true;
      continue;
    }
    if (arg == "--host") {
      config.host = ReadArg(argc, argv, i);
      if (config.host.empty()) {
        std::cerr << "--host requires a value\n";
        return 2;
      }
      continue;
    }
    if (arg == "--port") {
      const std::string value = ReadArg(argc, argv, i);
      if (value.empty()) {
        std::cerr << "--port requires a value\n";
        return 2;
      }
      config.port = std::stoi(value);
      continue;
    }
    if (arg == "--brosdk") {
      brosdk_library = ReadArg(argc, argv, i);
      if (brosdk_library.empty()) {
        std::cerr << "--brosdk requires a value\n";
        return 2;
      }
      continue;
    }

    std::cerr << "unknown argument: " << arg << "\n";
    PrintUsage();
    return 2;
  }

  bromcp::TaskStore tasks;
  bromcp::BrosdkClient client;
  if (!brosdk_library.empty()) {
    client.SetDefaultLibraryPath(brosdk_library);
  }
  client.SetEventHandler([&tasks](int32_t code, std::string event_name,
                                  std::string payload_json) {
    tasks.UpdateFromSdkEvent(code, event_name, payload_json);
  });

  bromcp::ToolRegistry tools;
  bromcp::RegisterBroTools(tools, client, tasks);

  bromcp::McpServer server(tools, tasks);
  if (stdio) {
    return server.RunStdio();
  }

  if (!server.Listen(config)) {
    std::cerr << "failed to listen on " << config.host << ":" << config.port
              << "\n";
    return 1;
  }

  return 0;
}
