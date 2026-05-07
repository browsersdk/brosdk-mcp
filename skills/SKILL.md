---
name: brosdk
description: Build, review, or update an OrbitBridge BroSDK skill or tool wrapper for OpenAI/Codex, Claude, MCP, or other agent runtimes. Use when designing safe LLM-facing browser-environment tools, adding Node/TypeScript adapters around brosdk.dll/brosdk.so/brosdk.dylib, exposing BroSDK C API or Web API operations as tools, or documenting how agents should initialize, open, close, query, and shut down BroSDK.
---

# BroSDK

## Overview

Use this skill to wrap OrbitBridge BroSDK as a safe agent-facing tool surface for OpenAI/Codex and Claude. Keep the C++ SDK core focused on browser execution, and put model/tool protocol behavior in a thin adapter layer.

## First Steps

1. Inspect the current public contract before changing behavior:
   - `brosdk.h` (the public C/C++ SDK header)
   - `docs/sdk-reference.md`
   - the BroSDK integration guide under `docs/`
   - `docs/brosdk_web_mcp_design.md`
2. Decide the target runtime:
   - OpenAI/Codex: read `references/openai.md`.
   - Claude or Claude Desktop/Code: read `references/claude.md`.
   - Cross-platform tool schema or MCP design: read `references/tool-design.md`.
   - C API/Web API details: read `references/api-surface.md`.
3. Keep one shared adapter core and add thin platform bindings on top. Do not fork business logic separately for OpenAI and Claude.
4. When updating the in-repo C++ MCP server, use `projects/bromcp/` as the adapter boundary. It should load the BroSDK runtime dynamically, acquire `ISDK*` through `sdk_init_cpp()`, and call the C++ virtual methods from `brosdk.h` for SDK operations.

## Architecture Rule

Prefer this layering:

```text
BroSDK C++ core
  -> C++ MCP adapter in projects/bromcp or Node/TypeScript adapter using C API FFI/local Web API
  -> shared tool/service layer with validation, task tracking, and redaction
  -> OpenAI tools and Claude/MCP tools
```

Do not add OpenAI, Claude, MCP, auth gateway, or prompt-template logic directly into `projects/brosdk` unless the user explicitly asks for a C++ protocol implementation. The SDK should expose capability; the adapter should expose policy.

## Tool Surface

Start with low-risk tools:

- `brosdk_health_check`
- `brosdk_init`
- `brosdk_open_browser_envs`
- `brosdk_close_browser_envs`
- `brosdk_get_env_info`
- `brosdk_netdiag`
- `brosdk_refresh_token`
- `brosdk_get_task`
- `brosdk_shutdown`

BroSDK environment administration tools may also be exposed when the adapter
adds validation and redaction:

- `brosdk_browser_install`
- `brosdk_create_env`
- `brosdk_update_env`
- `brosdk_page_env`
- `brosdk_env_getinfo`
- `brosdk_destroy_env`

Defer or gate sensitive tools:

- cookie details
- storage details
- arbitrary launch arguments
- unbounded URL lists
- bulk destructive operations

## Implementation Rules

- Treat every `envId` as a string. JavaScript numbers are unsafe for large environment IDs.
- Register `sdk_register_result_cb()` before `sdk_init()` when using the C API.
- For Web API async routes, establish WebSocket result handling before `browser/open`, `browser/close`, or `token/update`.
- Treat async return values where `sdk_is_done(code)` or `sdk_is_reqid(code)` is true as "accepted", not "finished".
- Use callback/WebSocket JSON as the source of truth for `reqId`, event type, and final status.
- Free all SDK-owned synchronous output buffers with `sdk_free()`.
- In callbacks, copy payloads immediately and return quickly. Do not call blocking SDK APIs from SDK callbacks.
- Validate and bound URLs, launch args, environment counts, and requested operations before calling BroSDK.
- Redact or summarize cookies/storage by default. Raw sensitive data requires an explicit user-approved capability.

## Output Contract

Expose stable business-level results instead of raw SDK JSON where possible:

```json
{
  "ok": true,
  "taskId": "adapter-task-id",
  "sdkReqId": "sdk-reqid-or-null",
  "status": "running",
  "message": "browser open task accepted"
}
```

Use a task store for async operations:

```json
{
  "taskId": "uuid-or-reqid-string",
  "sourceReqId": "sdk-reqid",
  "toolName": "brosdk_open_browser_envs",
  "status": "pending|running|succeeded|failed|cancelled",
  "progress": 0,
  "result": {},
  "error": null
}
```

## Validation

For documentation-only skill changes, run the skill validator:

```bash
python C:/Users/Administrator/.codex/skills/.system/skill-creator/scripts/quick_validate.py docs/skills/brosdk
```

For wrapper implementation changes, also run the relevant Node/TypeScript tests or a small local smoke test against the built BroSDK library.

For `projects/bromcp` changes, build the `bromcp` target and run at least:

```bash
bromcp --help
bromcp --stdio
```

Use a real `BROSDK_LIBRARY`/`--brosdk` path for end-to-end SDK smoke tests.
