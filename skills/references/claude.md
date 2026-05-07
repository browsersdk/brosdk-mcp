# Claude Integration

Use this reference when exposing BroSDK capabilities to Claude, Claude Desktop, Claude Code, or MCP-compatible clients.

## Skill Compatibility

Claude-style skills and Codex-style skills both use a `SKILL.md` entry point with YAML frontmatter. Keep the frontmatter limited to:

```yaml
---
name: brosdk
description: ...
---
```

Do not rely on OpenAI-only `agents/openai.yaml` for Claude. Claude should be able to use `SKILL.md` and the files in `references/` directly.

## MCP Binding Pattern

For Claude Desktop/Code, prefer a local MCP server over embedding Claude-specific logic in the SDK. The MCP server should call the same shared adapter layer used by OpenAI.

Recommended MCP tools:

- `brosdk_health_check`
- `brosdk_init`
- `brosdk_open_browser_envs`
- `brosdk_close_browser_envs`
- `brosdk_get_env_info`
- `brosdk_netdiag`
- `brosdk_refresh_token`
- `brosdk_get_task`

Recommended MCP resources:

- current SDK status
- running browser environment summary
- recent task list
- safe environment metadata

Recommended MCP prompts:

- safely open a browser environment
- close browser environments and verify completion
- inspect SDK health without exposing sensitive data

## Claude-Specific Guidance

- Keep each tool schema narrow. Claude tends to use available tools aggressively when descriptions are broad.
- Put safety constraints in the tool schema and server-side validation, not only in prompt text.
- Use `taskId` status polling for async operations instead of long blocking tool calls.
- Require explicit user intent for cookie/storage details, and return summaries unless raw access is explicitly enabled.
- Never echo `userSig`, proxy credentials, raw cookies, or raw storage values in a tool result.

## Local Wrapper Placement

If implementing the wrapper in this repo, prefer a separate adapter package such as:

```text
tools/bro-mcp-server/
  package.json
  src/
    index.ts
    sdk/brosdk.ts
    sdk/task-store.ts
    mcp/tools.ts
    mcp/resources.ts
    security/audit.ts
```

For an experimental prototype, `tests/bro-mcp-server/` is also acceptable. Avoid placing MCP protocol code inside `projects/brosdk`.
