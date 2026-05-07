# BroSDK API Surface

Use this reference when implementing or reviewing a wrapper over BroSDK's public C API or local Web API.

## Primary Sources

- `brosdk.h` is the final public C/C++ API source.
- `docs/sdk-reference.md` has request/response examples and event semantics.
- the BroSDK integration guide under `docs/` has integration flow and operational notes.

## C API Lifecycle

Preferred C API flow:

```text
1. sdk_register_result_cb()
2. optionally sdk_register_cookies_storage_cb()
3. sdk_init() or sdk_init_async()
4. sdk_browser_open()
5. wait for browser-open-success
6. sdk_browser_close()
7. wait for browser-close-success
8. sdk_shutdown()
```

## C++ MCP Adapter Lifecycle

`projects/bromcp` is the in-repo C++ MCP adapter. It should keep dynamic loading
at the boundary but call BroSDK through the C++ interface after the library is
loaded:

```text
1. Load brosdk.dll / brosdk.so / brosdk.dylib.
2. Resolve sdk_init_cpp(), sdk_free(), sdk_error_*(), and sdk_is_*().
3. Call sdk_init_cpp() to acquire ISDK*.
4. Register ISDK::RegisterResultCb() before ISDK::Init().
5. Call ISDK methods for operations:
   - Init
   - Info / BrowserInfo
   - NetworkDiagnostics
   - BrowserInstall / BrowserOpen / BrowserClose
   - CreateEnv / UpdateEnv / PageEnv / GetEnvInfo / DestroyEnv
   - UpdateToken
   - Shutdown
6. Free all synchronous output buffers with sdk_free().
```

Only the small ABI bootstrap and memory/status helpers should use C symbols.
Business operations should go through `ISDK` so the adapter matches `brosdk.h`.

Important exported functions:

- `sdk_register_result_cb`
- `sdk_register_cookies_storage_cb`
- `sdk_init`
- `sdk_init_async`
- `sdk_init_webapi`
- `sdk_info`
- `sdk_network_diagnostics`
- `sdk_browser_install`
- `sdk_browser_info`
- `sdk_browser_open`
- `sdk_browser_close`
- `sdk_env_create`
- `sdk_env_update`
- `sdk_env_page`
- `sdk_env_getinfo`
- `sdk_env_destroy`
- `sdk_token_update`
- `sdk_shutdown`
- `sdk_free`
- `sdk_malloc`
- `sdk_error_name`
- `sdk_error_string`
- `sdk_event_name`
- `sdk_is_error`
- `sdk_is_warn`
- `sdk_is_reqid`
- `sdk_is_ok`
- `sdk_is_done`
- `sdk_is_event`

## Memory And Callbacks

- All request and response bodies are UTF-8 JSON.
- `len` is byte length, not character count, and does not include a trailing null byte.
- Any synchronous output allocated by the SDK must be released with `sdk_free()`.
- If `sdk_cookies_storage_cb_t` replaces data, allocate the replacement with `sdk_malloc()`.
- `sdk_result_cb_t` payload pointers are callback-scoped. Copy immediately if the wrapper needs to retain them.
- Keep callbacks short and non-blocking. Push work into the adapter event loop or queue.

## Async Semantics

An async API return value means "accepted" when either condition is true:

- `sdk_is_done(code)`
- `sdk_is_reqid(code)`

The final result must come from the callback or WebSocket event payload. Do not treat `sdk_browser_open()` or `sdk_browser_close()` immediate return values as completion.

The callback's first `code` parameter is only a coarse status. Parse the JSON body for stable `reqId`, event type, and business data.

## Web API Surface

The local Web API is enabled through SDK initialization with a port or by `sdk_init_webapi()`. Main routes in the docs include:

- `POST /sdk/v1/init`
- `POST /sdk/v1/info`
- `POST /sdk/v1/browser/info`
- `POST /sdk/v1/browser/open`
- `POST /sdk/v1/browser/close`
- `POST /sdk/v1/token/update`
- `POST /sdk/v1/env/create`
- `POST /sdk/v1/env/update`
- `POST /sdk/v1/env/page`
- `POST /sdk/v1/env/destroy`
- `POST /sdk/v1/shutdown`

For async Web API routes, establish WebSocket handling before issuing the request, otherwise the caller may only receive the immediate ACK.

## Request Shape Notes

Use string environment IDs everywhere:

```json
{
  "envs": [
    {
      "envId": "2041695386304778240",
      "urls": ["https://example.com"],
      "args": []
    }
  ]
}
```

Close request:

```json
{
  "envs": ["2041695386304778240"]
}
```

Initialization typically carries:

```json
{
  "userSig": "signed-user-token",
  "workDir": "D:/OrbitBridge/bin/Debug/.brosdk",
  "port": 18765,
  "debug": false
}
```
