# OpenAI Integration

Use this reference when exposing BroSDK capabilities to OpenAI/Codex or OpenAI-style function tools.

## Skill Packaging

This skill uses the standard `SKILL.md` layout and includes `agents/openai.yaml` for OpenAI-facing UI metadata. Keep `agents/openai.yaml` short and deterministic:

- `display_name`: `BroSDK`
- `short_description`: concise UI label
- `default_prompt`: must mention `$brosdk`

## Tool Binding Pattern

Bind OpenAI tools to the shared adapter layer from `references/tool-design.md`.

Recommended first tool set:

- `brosdk_health_check`
- `brosdk_init`
- `brosdk_open_browser_envs`
- `brosdk_close_browser_envs`
- `brosdk_get_env_info`
- `brosdk_netdiag`
- `brosdk_refresh_token`

Use JSON schema constraints at the tool boundary:

- `envId`: string only.
- `envs`: bounded array.
- `urls`: bounded array of strings.
- `args`: omitted by default; allowlisted strings only.
- `userSig`: accepted as input but never returned.

## Response Style

Return concise structured objects suitable for tool-call consumption. For async SDK calls, return an accepted task object immediately and expose task status through a follow-up tool or resource.

Example accepted response:

```json
{
  "ok": true,
  "taskId": "task_01",
  "sdkReqId": "1594794915",
  "status": "running",
  "message": "browser open task accepted"
}
```

Example final status:

```json
{
  "ok": true,
  "taskId": "task_01",
  "status": "succeeded",
  "result": {
    "opened": ["2041695386304778240"]
  }
}
```

## OpenAI-Specific Guidance

- Keep tool descriptions business-level; do not expose raw C function names unless the user is building low-level bindings.
- Prefer safe summaries in tool outputs so the model does not ingest secrets by default.
- Add explicit confirmation gates before destructive or sensitive operations.
- Keep all platform-specific glue thin; the OpenAI binding should call the same adapter methods as Claude/MCP.
