# PROTOCOL REFERENCE

本文件描述 `brosdk-mcp` 当前对外协议语义（stdio / SSE / session / ARIA），作为实现与文档的对齐参考。

补充说明：

- `--cdp` 在启动时是可选参数。
- 提供 `--cdp` 时，服务会在启动阶段直接建立默认浏览器环境。
- 不提供 `--cdp` 时，服务仍可正常启动，但初始没有浏览器连接；后续需通过 `browser_connect_environment` 或 `browser_switch_environment` 接入浏览器。

## 1. 传输模式

- `stdio`：JSON-RPC 2.0 消息通过标准输入输出收发。
- `sse`：通过 HTTP + SSE 收发 MCP 请求与服务端广播。

### 1.1 交付态接入建议

- 交付环境优先使用预构建二进制或 release 包接入，而不是 `go run`。
- `stdio` 模式推荐直接由 Agent 拉起二进制：
  - 例如 `brosdk-mcp --mode stdio --cdp 127.0.0.1:9222`
  - 或 `brosdk-mcp --mode stdio`，后续再通过环境管理工具接入浏览器
- `sse` 模式推荐先独立启动服务，再由 Agent 通过 `/sse` URL 接入。
- 示例配置参考：
  - `examples/mcp/agent-stdio.example.json`：显式二进制路径
  - `examples/mcp/agent-stdio-binary.example.json`：二进制已在 `PATH`
  - `examples/mcp/agent-sse.example.json`：SSE 接入

## 2. JSON-RPC 约定

- 协议版本：`"jsonrpc": "2.0"`
- 主要方法：
  1. `tools/list`
  2. `tools/call`
- 解析失败返回 `-32700`（parse error）。

## 3. SSE 端点

### 3.1 `GET /sse`

用途：建立 SSE 流并注册（或复用）会话。

- 可选 query：`sessionId=<id>`
- 可选 header：`X-MCP-Session-ID: <id>`
- 建立后服务端会先推送 `ready` 事件：

```text
event: ready
data: {"status":"connected","sessionId":"...","reused":false}
```

字段语义：
- `sessionId`：当前连接绑定的会话 ID。
- `reused`：`true` 表示该 `sessionId` 已存在并被复用，`false` 表示新建。

### 3.2 `POST /message`

用途：提交 JSON-RPC 请求。

- 可选 query/header：`sessionId`
- 当携带 `sessionId` 时：
  1. 仅接受"已注册"session。
  2. 未注册返回 `404 unknown sessionId`。
  3. 成功响应头回传 `X-MCP-Session-ID`。
  4. 响应会广播到同 session 下的 SSE 客户端。
- 当不携带 `sessionId` 时：
  1. 仍可直接返回 JSON-RPC 响应。
  2. 不参与 session 广播。

### 3.3 `DELETE /session`

用途：显式清理会话与其关联 SSE 客户端。

- 可通过 query/header 传 `sessionId`。
- 成功返回 `204 No Content`。
- 未找到会话返回 `404 unknown sessionId`。
- 删除后该会话不再可用于 `POST /message`，除非重新通过 `GET /sse` 建立。

## 4. Session 生命周期

- 服务端维护会话注册表。
- 会话无活跃客户端时，按 TTL 清理（当前实现为 5 分钟）。
- 显式 `DELETE /session` 可立即结束会话。

## 5. 建议调用顺序（SSE）

1. `GET /sse?sessionId=<id>` 建连并读取 `ready`。
2. `POST /message?sessionId=<id>` 连续发送请求。
3. 结束后可调用 `DELETE /session?sessionId=<id>` 主动清理。

## 6. 健壮性策略（当前实现）

- 允许“无初始浏览器”启动：服务可以在没有 `--cdp` 的情况下先启动，再由运行期工具补充浏览器环境。
- CDP discovery（`/json/version`、`/json/list`）对瞬态错误做重试：
  1. 网络错误
  2. `429/502/503/504`
- CDP WebSocket 建连采用指数退避重试。
- 执行器关键页面调用在检测到连接丢失时，会按当前 `tabId` 自动重连并重试一次。
- 执行器内置轻量重连熔断窗口：短时间连续重连过多会暂时阻断，避免断连风暴下的高频重连。
- 导航等待采用"事件优先 + 状态兜底"；`networkidle` 在缺失 lifecycle 事件时具备受控兜底语义。
- 若导航等待中的事件订阅通道异常关闭，等待逻辑会退回 `waitForLoadState` 路径继续判定，避免直接失败。

## 7. ARIA 快照协议

### 7.1 `browser_aria_snapshot` 参数

| 参数          | 类型    | 默认值 | 说明                                                   |
|---------------|---------|--------|--------------------------------------------------------|
| `selector`    | string  | `""`   | 限定快照范围的 CSS selector；空时对整页生成快照         |
| `interactive` | boolean | `true` | `true` 只包含可交互元素；`false` 同时包含语义结构节点   |
| `compact`     | boolean | `true` | `true` 省略 `backendNodeId` 等调试字段                 |
| `maxDepth`    | integer | `24`   | 最大遍历深度（1–64）                                    |
| `tabId`       | string  | `""`   | 指定标签页；空时使用当前激活标签页                      |

### 7.2 快照生成路径

```
browser_aria_snapshot
  ├── selector == ""  →  AX 路径（优先）
  │     ├── Accessibility.getFullAXTree(pierce=true)
  │     ├── mergeIframeAXNodes（同源 iframe 合并）
  │     └── 失败 → DOM 注入路径（回退）
  └── selector != ""  →  AX selector 路径（优先）
        ├── DOM.getDocument → DOM.querySelector → DOM.describeNode
        ├── Accessibility.queryAXTree(backendNodeId)
        └── 失败 → DOM 注入路径（回退）
```

### 7.3 跨 frame / iframe 合并策略

1. `Accessibility.getFullAXTree(pierce=true)` 覆盖 shadow DOM，但不覆盖 iframe。
2. 对 AX 树中 `frameId` 非空的节点，尝试两种方式获取子树：
   - **Approach A**：`Accessibility.getFullAXTree(frameId=<id>)`（同进程 iframe，Chrome ≥ 112）。
   - **Approach B**：`Target.getTargets` 找到匹配 `targetId == frameId` 的目标，`Target.attachToTarget` 建立临时 session 后获取。
3. 子树节点 ID 加 `frame:<frameId>:` 前缀，避免与主树冲突。
4. 跨域或沙箱 iframe 获取失败时静默跳过，不中断整体快照。

### 7.4 ref 语义

- 每次 `browser_aria_snapshot` 调用都会重置 `window.__ariaRefs` 和 `window.__ariaRefMeta`。
- ref 格式：`e<N>`（如 `e1`、`e42`），在单次快照内唯一。
- `__ariaRefMeta` 存储 `{ role, name, nth, backendNodeId?, frameId? }` 等最小元信息，用于节点失联时的重定位与跨 frame 操作。
- 导航后或切换标签页后，ref 失效，需重新调用 `browser_aria_snapshot`。
- 同页重渲染（如 React/Vue 更新）场景：`by_ref` 工具会优先在当前页面按元信息自动重定位，降低短时失效率。
- 当 ref 元信息带有 `backendNodeId + frameId` 时，`browser_click_by_ref`、`browser_type_by_ref`、`browser_set_input_value_by_ref` 会在页面内 JS 路径失败后，回退到 CDP `DOM.resolveNode + Runtime.callFunctionOn` 路径。
- 对 iframe / OOPIF 场景，执行器会优先尝试附着到对应 frame target 的 session 后再执行节点操作；若不存在独立 frame target，则回退到当前 page session 尝试解析。

### 7.5 快照输出格式

```
- document "<title>"
  - <role> "<name>" [ref=<eN>]
  - <role> "<name>" [ref=<eN>] (backendNodeId=<id>, value=<v>)  ← compact=false 时
```

响应字段：
- `source`：`"ax"` 表示来自 AX 树路径，`"dom"` 表示来自 DOM 注入路径。
- `refCount`：本次快照中可操作 ref 的数量。

## 8. 版本与构建

- 版本号通过 `-ldflags "-X main.Version=<ver>"` 在构建时注入，启动日志中输出 `version` 字段。
- 本地构建：`make build`（当前平台）或 `make build-all`（全平台交叉编译）。
- 发布产物约定见 `.goreleaser.yaml`；使用 `goreleaser release` 生成多平台二进制与 checksums。
