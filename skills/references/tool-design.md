# Tool Design

Use this reference when designing the shared tool layer used by OpenAI and Claude wrappers.

## Shared Adapter Contract

Create one internal service layer with methods such as:

- `healthCheck()`
- `init(input)`
- `openBrowserEnvs(input)`
- `closeBrowserEnvs(input)`
- `getEnvInfo(input)`
- `refreshToken(input)`
- `getTask(input)`
- `shutdown(input)`
- `browserInstall(input)`
- `createEnv(input)`
- `updateEnv(input)`
- `pageEnv(input)`
- `destroyEnv(input)`

Then bind that layer to OpenAI tools, Claude/MCP tools, HTTP routes, or tests. The platform binding should only translate schemas and output format.

## First-Version Tools

### brosdk_health_check

Purpose: report whether the SDK library can be loaded, whether the SDK is initialized, version/platform data when available, and adapter health.

Output should never include secrets.

### brosdk_init

Purpose: initialize the SDK and register callback/WebSocket result handling.

Inputs:

- `userSig`: required unless the adapter is configured with a secure token provider.
- `workDir`: optional.
- `port`: optional when enabling Web API/WS.
- `debug`: optional boolean.

### brosdk_open_browser_envs

Purpose: open one or more browser environments.

Inputs:

- `envs`: non-empty bounded array.
- each item must have `envId` as string.
- `urls` must be bounded and validated.
- `args` must be either omitted or validated against an allowlist.

Return an accepted task, then update final state from SDK events.

### brosdk_close_browser_envs

Purpose: close one or more browser environments.

Inputs:

- `envs`: non-empty bounded string array.

Return an accepted task, then update final state from SDK events.

### brosdk_get_env_info

Purpose: return SDK/browser/environment summary state. Prefer summaries over raw backend payloads.

### brosdk_refresh_token

Purpose: refresh `userSig` or other SDK auth material. Never echo token values.

### brosdk_browser_install

Purpose: install or update browser core resources through the SDK async path.
Return an accepted task and update progress from callback events.

### brosdk_create_env / brosdk_update_env / brosdk_page_env

Purpose: pass validated environment administration requests to the synchronous
BroSDK environment APIs. Return redacted backend JSON.

### brosdk_destroy_env

Purpose: destroy an environment. Require explicit confirmation in tool input and
return a redacted result.

## Tool Input Guardrails

- Keep all IDs as strings.
- Set maximum `envs` count.
- Set maximum `urls` count per environment.
- Reject file URLs and private/internal URLs unless the operator explicitly enables them.
- Reject unrecognized Chromium args.
- Require an ownership or tenancy check before acting on an env ID in remote or shared deployments.
- Record a safe audit summary for each mutating call.

## Tool Output Guardrails

Use stable wrappers:

```json
{
  "ok": true,
  "taskId": "adapter-task-id",
  "sdkReqId": "sdk-reqid-or-null",
  "status": "accepted|running|succeeded|failed",
  "message": "short human-readable status"
}
```

For errors:

```json
{
  "ok": false,
  "error": {
    "code": "BROSDK_INIT_FAILED",
    "message": "safe summary",
    "retryable": false
  }
}
```

Do not leak:

- `userSig`
- raw cookie values
- raw storage values
- local full paths unless the user is working locally and needs diagnostics
- proxy credentials

## Task Store

Maintain a task object for async operations:

```json
{
  "taskId": "uuid-or-reqid-string",
  "sourceReqId": "sdk-reqid",
  "toolName": "brosdk_open_browser_envs",
  "status": "pending|running|succeeded|failed|cancelled",
  "progress": 0,
  "message": "",
  "result": {},
  "error": null,
  "createdAt": "ISO-8601",
  "updatedAt": "ISO-8601"
}
```

Use the SDK event stream to update this store. Do not block tool calls waiting forever for final events.
