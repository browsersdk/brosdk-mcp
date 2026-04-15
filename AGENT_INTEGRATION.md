# AGENT MCP INTEGRATION

本文用于指导 AI Agent 以“打包交付物”方式接入 `brosdk-mcp`。

适用范围：
- 任意支持 MCP 的 Agent/客户端
- 支持 `stdio` 或 `sse` 两种接入方式
- 优先面向已构建或已发布的二进制交付物，而不是开发态 `go run`

## 1. 推荐接入方式

推荐顺序：
1. 使用预构建二进制或 release 包接入
2. 本地自行构建二进制后接入
3. 仅在开发调试时使用 `go run`

原因：
- 二进制接入更接近真实交付形态
- 不要求 Agent 所在机器具备 Go 开发环境
- 启动更快，配置更稳定
- 更容易和版本、发布、回归验证对齐

## 2. 前置条件

1. 已获取 `brosdk-mcp` 可执行文件
2. 本机可启动 Chrome/Chromium，并开启远程调试端口
3. 确认 Agent 运行环境可以访问该二进制与 CDP 端口

Windows 示例：

```powershell
chrome --remote-debugging-port=9222
```

## 3. 获取交付物

### 3.1 直接使用发布产物

如果你已经有 release 包，解压后直接使用其中的可执行文件：

- Windows: `brosdk-mcp.exe`
- Linux/macOS: `brosdk-mcp`

建议把它放到：
- Agent 可直接引用的固定路径
- 或系统 `PATH` 中

### 3.2 本地构建交付物

如果当前机器有源码仓库，可先构建再接入：

```powershell
make build
```

或：

```powershell
go build -o ./brosdk-mcp.exe ./cmd/brosdk-mcp
```

构建完成后，优先使用生成的二进制接入，而不是让 Agent 直接执行 `go run`。

## 4. 启动 MCP 服务

### 4.1 `stdio` 模式

推荐给本地 Agent。

```powershell
.\brosdk-mcp.exe --mode stdio --cdp 127.0.0.1:9222
```

### 4.2 `sse` 模式

推荐给远程 Agent、需要复用服务、或多客户端场景。

```powershell
.\brosdk-mcp.exe --mode sse --cdp 127.0.0.1:9222 --port 8080
```

## 5. Agent 自动接入配置模板

不同 Agent 的配置文件路径不一样，但多数都支持 `mcpServers` 结构。

可直接参考：
- `examples/mcp/agent-stdio-binary.example.json`
- `examples/mcp/agent-stdio.example.json`
- `examples/mcp/agent-sse.example.json`

### 5.1 推荐：`stdio` 二进制模板

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

如果可执行文件已在 `PATH` 中，也可以写成：

```json
{
  "mcpServers": {
    "brosdk-mcp": {
      "command": "brosdk-mcp",
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

### 5.2 `sse` 模板

先独立启动 `brosdk-mcp` 服务，再让 Agent 通过 URL 接入：

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

### 5.3 开发态模板

仅用于本地开发调试，不建议作为交付配置：

```json
{
  "mcpServers": {
    "brosdk-mcp": {
      "command": "go",
      "args": [
        "run",
        "./cmd/brosdk-mcp",
        "--mode",
        "stdio",
        "--cdp",
        "127.0.0.1:9222"
      ],
      "cwd": "D:/work/brosdk-mcp"
    }
  }
}
```

## 6. 交付态验证

建议按下面顺序验证：

1. 手动启动二进制
2. 让 Agent 调用 `tools/list`
3. 让 Agent 依次调用：
   - `browser_navigate`
   - `browser_aria_snapshot`
4. 如使用 SSE，再验证：
   - `GET /sse?sessionId=<id>`
   - `POST /message?sessionId=<id>`
   - 完成后 `DELETE /session?sessionId=<id>`

推荐本地验证命令：

```powershell
go test ./...
go build ./cmd/brosdk-mcp
```

如需多平台交付物：

```powershell
make build-all
```

## 7. 常见问题

1. Agent 无法启动 MCP
   - 检查 `command` 是否指向真实二进制路径
   - 检查该路径是否对 Agent 进程可见
   - 先手动运行同一命令，确认可执行文件本身可启动

2. 机器上没有 Go
   - 使用预构建二进制，不要使用 `go run`

3. Chrome 连接失败
   - 确认 Chrome 已开启 `--remote-debugging-port=9222`
   - 检查 `--cdp` 参数与实际端口是否一致

4. SSE 返回 `404 unknown sessionId`
   - 先建立 `GET /sse?sessionId=...`
   - 再调用 `POST /message?sessionId=...`
   - 如果 session 已清理，需要重新建立

## 8. 相关文档

- 协议语义：`PROTOCOL.md`
- 主文档：`README.md`
- 项目说明：`README.md`
