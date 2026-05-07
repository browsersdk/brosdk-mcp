# BroSDK MCP + Skills

> 中文 | [English](#english)

BroSDK MCP + Skills 是一个面向 AI Agent 的 BroSDK 适配层。它把 BroSDK 的浏览器环境能力封装成 MCP 工具，并附带一份可安装到 Codex / OpenAI / Claude 工作流中的 BroSDK Skill，用于指导模型以安全、可控、可审计的方式初始化 SDK、打开/关闭浏览器环境、查询环境、执行网络诊断和跟踪异步任务。

当前仓库包含两部分：

```text
brosdk-mcp/
  bromcp/    C++ MCP / JSON-RPC 适配器
  skills/    BroSDK Agent Skill 与参考文档
```

## 项目状态

当前实现适合作为开源预览版或内部集成起点：

- `bromcp` 已实现 stdio MCP 与 WebSocket JSON-RPC 两种传输方式。
- `bromcp` 通过动态库加载 BroSDK，不把 BroSDK runtime 静态打包进 MCP 适配器。
- `skills/` 提供 BroSDK 的安全调用规则、工具面设计、OpenAI / Claude 接入参考。
- README 中的构建方式按当前源码结构说明；如果你要把它做成完全独立的开源包，建议补充 CI、License、Release packaging 和第三方依赖获取脚本。

## 功能概览

`bromcp` 暴露的主要能力：

| 工具 | 说明 |
| --- | --- |
| `brosdk_health_check` | 检查 MCP 适配器和 BroSDK 动态库加载状态 |
| `brosdk_init` | 初始化 BroSDK，可传 `userSig`、`workDir`、`port` 等 |
| `brosdk_browser_install` | 安装浏览器核心资源，返回异步任务 |
| `brosdk_open_browser_envs` | 打开一个或多个浏览器环境，返回异步任务 |
| `brosdk_close_browser_envs` | 关闭一个或多个浏览器环境，返回异步任务 |
| `brosdk_get_env_info` | 获取 SDK 与当前运行中浏览器摘要 |
| `brosdk_create_env` | 透传创建环境请求 |
| `brosdk_update_env` | 透传更新环境请求 |
| `brosdk_page_env` | 透传分页查询环境请求 |
| `brosdk_env_getinfo` | 查询单个环境完整信息 |
| `brosdk_destroy_env` | 销毁环境，要求显式 `confirmDestroy: true` |
| `brosdk_refresh_token` | 刷新 `userSig`，返回异步任务 |
| `brosdk_netdiag` | 同步执行网络 / 代理诊断 |
| `brosdk_get_task` | 查询异步任务状态 |
| `brosdk_shutdown` | 停止 SDK 单例 |

异步工具只表示 SDK 已受理任务，最终状态需要通过 `brosdk_get_task` 查询。SDK 回调事件会进入内置 task store。

## 安全边界

这个项目默认把 BroSDK 作为高权限本地能力处理，因此做了几条保守约束：

- `envId` 始终按字符串处理，避免大整数在 JavaScript / JSON 客户端中丢精度。
- 打开浏览器时限制单次环境数量、URL 数量和启动参数数量。
- 浏览器启动参数使用 allowlist，只允许明确安全的 Chromium 参数。
- 拒绝 `file:`、`data:`、`javascript:` 等危险 URL scheme。
- `destroy` 操作要求显式确认。
- 工具返回会对 `userSig`、token、cookie、storage、password、secret、proxy 等敏感字段做脱敏。
- 默认不提供 Cookie / Storage 明文读取能力。

如果你要开放给不可信模型或多人使用，建议再加一层鉴权、配额、审计日志和操作审批。

## 构建

### 前置条件

- CMake 3.16+
- C++20 编译器
- RapidJSON
- websocketpp + standalone Asio
- Windows 下需要 `dlfcn-win32` 或等效 `dlopen` 兼容层
- BroSDK public header：`brosdk.h`
- BroSDK runtime：`brosdk.dll` / `brosdk.so` / `brosdk.dylib`

当前 `bromcp/CMakeLists.txt` 是从 OrbitBridge 工程拆出的实现，默认依赖上层工程变量：

- `xs3RDPARTY_DIR`
- `xsBIN_DIR`
- BroSDK 头文件路径：`../brosdk/interface`

### 在 OrbitBridge 工程中构建

如果你把 `bromcp` 放在 OrbitBridge 工程中，推荐直接构建目标：

```powershell
cmake --build out --target bromcp --config Release
```

生成后运行：

```powershell
.\bin\Release\bromcp.exe --help
```

### 独立仓库构建

如果你要把本仓库独立开源，请确保目录或 CMake 配置能找到 BroSDK 头文件和第三方依赖。例如：

```powershell
cmake -S bromcp -B build/bromcp `
  -Dxs3RDPARTY_DIR=D:/path/to/3rdparty `
  -DxsBIN_DIR=D:/work/brosdk-mcp/bin

cmake --build build/bromcp --config Release
```

独立开源时建议把 CMake 改造成显式变量，例如 `BROSDK_INCLUDE_DIR`、`RAPIDJSON_INCLUDE_DIR`、`WEBSOCKETPP_INCLUDE_DIR`、`ASIO_INCLUDE_DIR`，这样用户不需要了解 OrbitBridge 的内部变量名。

## 运行

`bromcp` 不强制把 BroSDK 动态库放在固定位置。可以通过命令行或环境变量指定：

```powershell
$env:BROSDK_LIBRARY = "D:\path\to\brosdk.dll"
.\bromcp.exe --stdio
```

或：

```powershell
.\bromcp.exe --stdio --brosdk D:\path\to\brosdk.dll
```

默认不加 `--stdio` 时会启动 WebSocket / HTTP JSON-RPC 服务：

```powershell
.\bromcp.exe --host 127.0.0.1 --port 18766 --brosdk D:\path\to\brosdk.dll
```

可用端点：

| 路径 | 说明 |
| --- | --- |
| `GET /health` | 服务健康检查 |
| `GET /tools` | OpenAI 风格工具元数据 |
| `GET /mcp/tools` | MCP 工具元数据 |
| `POST /mcp` | HTTP JSON-RPC |
| `WS /mcp` | WebSocket JSON-RPC |
| `POST /tools/{toolName}` | 直接调用单个工具 |
| `GET /tasks` | 最近任务列表 |
| `GET /tasks/{taskId}` | 单个任务状态 |

## 调用示例

### 健康检查

```powershell
Invoke-RestMethod http://127.0.0.1:18766/health
```

### 初始化 SDK

```powershell
Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:18766/tools/brosdk_init `
  -ContentType "application/json" `
  -Body '{
    "userSig": "YOUR_USER_SIG",
    "workDir": "D:/BroSDK/work",
    "port": 9527,
    "debug": true
  }'
```

### 打开浏览器环境

```powershell
Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:18766/tools/brosdk_open_browser_envs `
  -ContentType "application/json" `
  -Body '{
    "envs": [
      {
        "envId": "2051156171976347648",
        "urls": ["https://example.com"],
        "args": ["--window-size=1280,720"]
      }
    ]
  }'
```

返回示例：

```json
{
  "ok": true,
  "taskId": "task_1",
  "sdkReqId": "1677842284",
  "status": "running",
  "message": "browser open task accepted"
}
```

随后查询：

```powershell
Invoke-RestMethod http://127.0.0.1:18766/tasks/task_1
```

## MCP 客户端配置

### Claude Desktop

示例 `claude_desktop_config.json`：

```json
{
  "mcpServers": {
    "brosdk": {
      "command": "D:\\work\\brosdk-mcp\\bin\\bromcp.exe",
      "args": ["--stdio"],
      "env": {
        "BROSDK_LIBRARY": "D:\\path\\to\\brosdk.dll"
      }
    }
  }
}
```

### Codex / OpenAI Skills

把 `skills/` 安装到你的 Codex skills 目录中，例如：

```text
%USERPROFILE%\.codex\skills\brosdk
```

安装后，模型可以通过 `$brosdk` skill 获得 BroSDK 工具调用边界、字段约束、异步任务语义和安全规则。

如果你使用自己的 Agent runtime，可以直接读取：

- `skills/SKILL.md`
- `skills/references/openai.md`
- `skills/references/claude.md`
- `skills/references/tool-design.md`
- `skills/references/api-surface.md`
- `skills/agents/openai.yaml`

## 推荐工作流

1. 调用 `brosdk_health_check`，确认 MCP server 可用。
2. 调用 `brosdk_init` 初始化 BroSDK。
3. 使用 `brosdk_netdiag` 验证 URL / 代理链路。
4. 调用 `brosdk_open_browser_envs` 打开环境。
5. 调用 `brosdk_get_task` 轮询直到 `succeeded` 或 `failed`。
6. 完成任务后调用 `brosdk_close_browser_envs`。
7. 最后按需调用 `brosdk_shutdown`。

## 开源发布清单

发布到 GitHub / npm / release 页面前，建议补齐：

- `LICENSE`
- 版本号与 changelog
- 可复现的独立 CMake 构建脚本
- CI：Windows / Linux / macOS 至少跑 `bromcp --help` 和基础 JSON-RPC smoke test
- Release 包：二进制、README、示例配置，不包含任何私有 `userSig`
- BroSDK runtime 获取方式说明
- 安全声明：本工具会控制本机浏览器环境，请只连接可信 MCP client

---

## English

BroSDK MCP + Skills is an AI-agent-facing adapter for BroSDK. It exposes BroSDK browser-environment operations as MCP tools and includes a BroSDK Skill for Codex / OpenAI / Claude workflows. The goal is to let agents initialize BroSDK, open and close browser environments, query environment state, run network diagnostics, and track asynchronous tasks with safe defaults.

Repository layout:

```text
brosdk-mcp/
  bromcp/    C++ MCP / JSON-RPC adapter
  skills/    BroSDK Agent Skill and reference material
```

## Status

This repository is suitable as an open-source preview or integration starting point:

- `bromcp` supports both stdio MCP and WebSocket JSON-RPC transports.
- `bromcp` loads the BroSDK runtime dynamically.
- `skills/` documents safe BroSDK usage, tool design, and OpenAI / Claude integration notes.
- The current CMake file comes from the OrbitBridge tree. For a fully standalone open-source package, consider adding CI, a license, release packaging, and explicit dependency discovery variables.

## Features

| Tool | Description |
| --- | --- |
| `brosdk_health_check` | Report adapter and BroSDK dynamic-library status |
| `brosdk_init` | Initialize BroSDK with `userSig`, `workDir`, `port`, etc. |
| `brosdk_browser_install` | Install browser core resources and create an async task |
| `brosdk_open_browser_envs` | Open one or more browser environments |
| `brosdk_close_browser_envs` | Close one or more browser environments |
| `brosdk_get_env_info` | Return SDK and running-browser summaries |
| `brosdk_create_env` | Forward an environment creation request |
| `brosdk_update_env` | Forward an environment update request |
| `brosdk_page_env` | Query environment pages |
| `brosdk_env_getinfo` | Fetch full details for one environment |
| `brosdk_destroy_env` | Destroy an environment; requires `confirmDestroy: true` |
| `brosdk_refresh_token` | Refresh `userSig` and create an async task |
| `brosdk_netdiag` | Run synchronous network / proxy diagnostics |
| `brosdk_get_task` | Read async task status |
| `brosdk_shutdown` | Stop the SDK singleton |

Async tools mean "accepted", not "finished". Poll `brosdk_get_task` or `/tasks/{taskId}` for final state.

## Safety Model

BroSDK can control local browser environments, so this adapter uses conservative defaults:

- Treat every `envId` as a string.
- Bound the number of environments, URLs, and browser arguments per request.
- Use a browser-argument allowlist.
- Reject dangerous URL schemes such as `file:`, `data:`, and `javascript:`.
- Require explicit confirmation before destroying environments.
- Redact `userSig`, tokens, cookies, storage, passwords, secrets, and proxy fields in returned SDK JSON.
- Do not expose raw Cookie / Storage content by default.

For untrusted or multi-user deployments, add authentication, quotas, audit logs, and approval gates.

## Build

### Requirements

- CMake 3.16+
- A C++20 compiler
- RapidJSON
- websocketpp + standalone Asio
- `dlfcn-win32` or an equivalent `dlopen` compatibility layer on Windows
- BroSDK public header: `brosdk.h`
- BroSDK runtime: `brosdk.dll`, `brosdk.so`, or `brosdk.dylib`

The current `bromcp/CMakeLists.txt` expects OrbitBridge-style variables and paths:

- `xs3RDPARTY_DIR`
- `xsBIN_DIR`
- BroSDK headers at `../brosdk/interface`

### Build Inside OrbitBridge

```powershell
cmake --build out --target bromcp --config Release
```

Then:

```powershell
.\bin\Release\bromcp.exe --help
```

### Standalone Build

For a standalone checkout, make sure the BroSDK headers and third-party dependencies are discoverable:

```powershell
cmake -S bromcp -B build/bromcp `
  -Dxs3RDPARTY_DIR=D:/path/to/3rdparty `
  -DxsBIN_DIR=D:/work/brosdk-mcp/bin

cmake --build build/bromcp --config Release
```

For a polished open-source release, consider replacing those internal variable names with explicit options such as `BROSDK_INCLUDE_DIR`, `RAPIDJSON_INCLUDE_DIR`, `WEBSOCKETPP_INCLUDE_DIR`, and `ASIO_INCLUDE_DIR`.

## Run

Specify the BroSDK runtime with either `BROSDK_LIBRARY` or `--brosdk`:

```powershell
$env:BROSDK_LIBRARY = "D:\path\to\brosdk.dll"
.\bromcp.exe --stdio
```

or:

```powershell
.\bromcp.exe --stdio --brosdk D:\path\to\brosdk.dll
```

Without `--stdio`, `bromcp` starts a local HTTP / WebSocket JSON-RPC service:

```powershell
.\bromcp.exe --host 127.0.0.1 --port 18766 --brosdk D:\path\to\brosdk.dll
```

Available endpoints:

| Endpoint | Description |
| --- | --- |
| `GET /health` | Health check |
| `GET /tools` | OpenAI-style tool metadata |
| `GET /mcp/tools` | MCP tool metadata |
| `POST /mcp` | HTTP JSON-RPC |
| `WS /mcp` | WebSocket JSON-RPC |
| `POST /tools/{toolName}` | Direct single-tool call |
| `GET /tasks` | Recent tasks |
| `GET /tasks/{taskId}` | One task by ID |

## Examples

### Health Check

```powershell
Invoke-RestMethod http://127.0.0.1:18766/health
```

### Initialize BroSDK

```powershell
Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:18766/tools/brosdk_init `
  -ContentType "application/json" `
  -Body '{
    "userSig": "YOUR_USER_SIG",
    "workDir": "D:/BroSDK/work",
    "port": 9527,
    "debug": true
  }'
```

### Open a Browser Environment

```powershell
Invoke-RestMethod `
  -Method Post `
  -Uri http://127.0.0.1:18766/tools/brosdk_open_browser_envs `
  -ContentType "application/json" `
  -Body '{
    "envs": [
      {
        "envId": "2051156171976347648",
        "urls": ["https://example.com"],
        "args": ["--window-size=1280,720"]
      }
    ]
  }'
```

Poll the task:

```powershell
Invoke-RestMethod http://127.0.0.1:18766/tasks/task_1
```

## MCP Client Configuration

### Claude Desktop

Example `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "brosdk": {
      "command": "D:\\work\\brosdk-mcp\\bin\\bromcp.exe",
      "args": ["--stdio"],
      "env": {
        "BROSDK_LIBRARY": "D:\\path\\to\\brosdk.dll"
      }
    }
  }
}
```

### Codex / OpenAI Skills

Install `skills/` as a Codex skill, for example:

```text
%USERPROFILE%\.codex\skills\brosdk
```

The skill helps models respect BroSDK safety boundaries, async task semantics, string `envId` handling, and redaction rules.

Useful files:

- `skills/SKILL.md`
- `skills/references/openai.md`
- `skills/references/claude.md`
- `skills/references/tool-design.md`
- `skills/references/api-surface.md`
- `skills/agents/openai.yaml`

## Recommended Agent Workflow

1. Call `brosdk_health_check`.
2. Call `brosdk_init`.
3. Use `brosdk_netdiag` before opening a browser environment.
4. Call `brosdk_open_browser_envs`.
5. Poll `brosdk_get_task` until the task is `succeeded` or `failed`.
6. Call `brosdk_close_browser_envs`.
7. Call `brosdk_shutdown` when the session is done.

## Open-Source Release Checklist

Before publishing this repository publicly, consider adding:

- `LICENSE`
- versioning and changelog
- standalone CMake dependency discovery
- CI for Windows / Linux / macOS
- smoke tests for `bromcp --help` and JSON-RPC tool calls
- release packages that do not contain private `userSig` values
- instructions for obtaining the BroSDK runtime
- a security note: this tool controls local browser environments and should only be connected to trusted MCP clients
