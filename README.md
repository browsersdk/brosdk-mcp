# brosdk-mcp

`brosdk-mcp` is a Go-based MCP server for browser automation. It can connect to Chrome or Chromium through the Chrome DevTools Protocol (CDP) at startup, or start without an initial browser and let agents attach environments later. It exposes a practical browser toolset to AI agents over `stdio` or `SSE`.

The project focuses on reliable page interaction, ARIA-based snapshots, tab management, waits, screenshots, JavaScript evaluation, and multi-environment browser routing.

## Features

- Supports `stdio` and `SSE` transport modes.
- Can connect to an existing Chrome/Chromium instance via `--remote-debugging-port`.
- Exposes 32 browser tools through MCP.
- Provides ARIA snapshots with `ref` identifiers for follow-up `by_ref` actions.
- Supports Shadow DOM traversal and frame-aware fallback for ref-based actions.
- Supports multiple browser environments in one MCP server process.
- Includes embedded tool schema and example MCP client configs.

## Tooling Overview

Current tools:

- Navigation: `browser_navigate`, `browser_reload`, `browser_go_back`, `browser_go_forward`
- Inspection: `browser_aria_snapshot`, `browser_screenshot`, `browser_get_text`, `browser_evaluate`
- Interaction: `browser_click`, `browser_click_by_ref`, `browser_type`, `browser_type_by_ref`, `browser_set_input_value`, `browser_set_input_value_by_ref`, `browser_find_and_click_text`, `browser_press`, `browser_scroll`
- Waiting: `browser_wait`, `browser_wait_for_selector`, `browser_wait_for_text`, `browser_wait_for_load`, `browser_wait_for_url`, `browser_wait_for_function`
- Tabs: `browser_list_tabs`, `browser_new_tab`, `browser_switch_tab`, `browser_close_tab`
- Environments: `browser_connect_environment`, `browser_launch_environment`, `browser_list_environments`, `browser_switch_environment`, `browser_close_environment`

The canonical machine-readable registry is:

- `internal/schema/browser-tools.schema.json`
- `schemas/browser-tools.schema.json`

## Requirements

- Go `1.25.1` or newer for local build
- Chrome or Chromium with remote debugging enabled if you want an initial browser connection at startup

Example:

```bash
chrome --remote-debugging-port=9222
```

On Windows:

```powershell
& "C:\Program Files\Google\Chrome\Application\chrome.exe" --remote-debugging-port=9222
```

## Build

Build for the current platform:

```bash
go build -o brosdk-mcp ./cmd/brosdk-mcp
```

Or use the provided Makefile:

```bash
make build
```

Cross-build common release targets:

```bash
make build-all
```

## Run

### stdio

```bash
brosdk-mcp --mode stdio --cdp 127.0.0.1:9222
```

Or start without an initial browser:

```bash
brosdk-mcp --mode stdio
```

### SSE

```bash
brosdk-mcp --mode sse --cdp 127.0.0.1:9222 --port 8080
```

Or:

```bash
brosdk-mcp --mode sse --port 8080
```

When running in `sse` mode, the server prints:

- SSE endpoint: `http://127.0.0.1:8080/sse`
- message endpoint: `http://127.0.0.1:8080/message`

## CLI Flags

| Flag | Description |
| --- | --- |
| `--mode` | `stdio` or `sse`, default `stdio` |
| `--cdp` | Optional Chrome CDP endpoint, such as `127.0.0.1:9222` or `ws://...` |
| `--name` | Default environment name used when `--cdp` is provided, default `default` |
| `--port` | HTTP port for `sse` mode, `<=0` means auto assign |
| `--log-file` | Optional log file path |
| `--low-injection` | Prefer CDP-native actions and reduce JS-heavy fallbacks |
| `--schema` | Deprecated compatibility flag, ignored because schema is embedded |

## MCP Client Configuration

### stdio example

See [`examples/mcp/agent-stdio.example.json`](examples/mcp/agent-stdio.example.json):

```json
{
  "mcpServers": {
    "brosdk-mcp": {
      "command": "D:/tools/brosdk-mcp/brosdk-mcp.exe",
      "args": [
        "--mode",
        "stdio",
        "--cdp",
        "127.0.0.1:9222"
      ]
    }
  }
}
```

### SSE example

See [`examples/mcp/agent-sse.example.json`](examples/mcp/agent-sse.example.json):

```json
{
  "mcpServers": {
    "brosdk-mcp": {
      "transport": "sse",
      "url": "http://127.0.0.1:8080/sse"
    }
  }
}
```

## Recommended Usage Flow

1. Start Chrome or Chromium with remote debugging enabled.
2. Start `brosdk-mcp` in `stdio` or `sse` mode, with or without `--cdp`.
3. If you started without `--cdp`, connect a browser later with `browser_connect_environment` or `browser_switch_environment`.
4. Call `tools/list` to inspect the available tool set.
5. Use `browser_navigate` to open the target page.
6. Use `browser_aria_snapshot` to obtain a stable, accessibility-oriented snapshot with refs.
7. Use `browser_click_by_ref`, `browser_type_by_ref`, or `browser_set_input_value_by_ref` for follow-up actions when possible.
8. Use `browser_wait_for_*`, `browser_get_text`, `browser_screenshot`, and `browser_evaluate` to validate page state.

## ARIA Snapshot Notes

- `browser_aria_snapshot` returns an accessibility-oriented tree and assigns `ref` values like `e1`, `e2`, `e3`.
- `by_ref` tools are intended for follow-up actions on those refs.
- Refs should be treated as page-state scoped. After navigation or tab switch, generate a fresh snapshot.
- The implementation prefers AX-tree data and falls back to DOM injection when needed.

## Multi-Environment Support

`brosdk-mcp` can keep multiple browser environments and switch between them.

If `--cdp` is omitted at startup, this is the main way to attach a browser later.

Typical flow:

1. `browser_connect_environment`
2. `browser_launch_environment`
3. `browser_list_environments`
4. `browser_switch_environment`
5. Call regular browser tools with the active environment, or pass `environment` on supported calls for one-off targeting

`browser_launch_environment` can launch a local Chrome/Chromium process for you. If `name` is omitted, brosdk-mcp will assign one automatically such as `local`, `local-1`, or `local-2`.

## PageAgent Plan

The next major direction is introducing a `PageAgent` concept for AI agents that operate on a specific page.

This is separate from the current browser MCP tools. The intended design is:

1. Strengthen the page runtime inside the MCP server
2. Introduce page-scoped agent state and memory
3. Add controlled PageAgent MCP tools
4. Expand toward multi-step and eventually autonomous page-level execution

Planned layers:

- Page runtime:
  - page identity
  - page-scoped CDP session
  - page-scoped refs, waits, and cached page state
- AI PageAgent:
  - page goal
  - page-scoped memory
  - action planning
  - controlled tool execution on one page

Planned MCP surface for the first PageAgent iteration:

- `browser_create_page_agent`
- `browser_list_page_agents`
- `browser_get_page_agent`
- `browser_run_page_agent_step`
- `browser_remove_page_agent`

Implementation plan:

1. Introduce internal page runtime objects without changing existing browser tool semantics
2. Migrate tab and page state out of the global executor into page-scoped runtime structures
3. Add PageAgent registry and state model
4. Add step-based PageAgent execution before attempting autonomous loops

The current recommendation is to keep `PageAgent` as an AI-agent concept while continuing to expose browser-native concepts like `tab` and `page` in the public MCP API where possible.

## Development

Run unit tests:

```bash
go test ./...
```

Run E2E tests with a local Chrome available:

```bash
BROSDK_E2E=1 go test ./internal/e2e/... -v -timeout 120s
```

Run `go vet`:

```bash
go vet ./...
```

## Release

This repository includes a `.goreleaser.yaml` for GitHub release packaging.

Typical release flow:

```bash
git tag v0.1.0
goreleaser release
```

The release archives include:

- `README.md`
- `PROTOCOL.md`
- `AGENT_INTEGRATION.md`
- `schemas/browser-tools.schema.json`
- `examples/**`

## Troubleshooting

### Cannot connect to Chrome

- If you started without `--cdp`, this is expected until you add or select an environment.
- Confirm Chrome was started with `--remote-debugging-port`.
- Confirm `http://127.0.0.1:9222/json/version` is reachable.
- Confirm the `--cdp` value points to the correct host and port.

### `stdio` mode seems silent

- MCP responses are written to standard output as JSON-RPC.
- Logs are written to standard error, or to `--log-file` if configured.

### `by_ref` actions stop working

- Generate a fresh `browser_aria_snapshot`.
- Refs are not stable across navigation, page reload, or major DOM re-render.

## Related Docs

- [PROTOCOL.md](PROTOCOL.md): transport, SSE session, and ARIA protocol details
- [AGENT_INTEGRATION.md](AGENT_INTEGRATION.md): agent/client integration examples
