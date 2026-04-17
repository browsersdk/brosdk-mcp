package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"brosdk-mcp/internal/cdp"
	"brosdk-mcp/internal/config"
	"brosdk-mcp/internal/mcp"
	"brosdk-mcp/internal/schema"
	"brosdk-mcp/internal/tools"
	"brosdk-mcp/internal/transports/sse"
	"brosdk-mcp/internal/transports/stdio"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger, buildVersion string) error {
	reg, err := schema.LoadEmbedded()
	if err != nil {
		return err
	}

	router := mcp.NewRouter(reg)

	logger.Info("Chrome MCP Server bootstrap complete",
		"version", buildVersion,
		"mode", cfg.Mode,
		"schemaVersion", reg.Version,
		"tools", router.ToolCount(),
	)

	var cdpClient *cdp.Client
	if cfg.CDPEndpoint != "" {
		wsURL, err := cdp.DiscoverWebSocketURL(ctx, cfg.CDPEndpoint)
		if err != nil {
			return err
		}

		cdpClient, err = cdp.NewClient(ctx, wsURL, logger)
		if err != nil {
			return err
		}
		defer cdpClient.Close()

		version, err := cdpClient.GetBrowserVersion(ctx)
		if err != nil {
			return err
		}
		logger.Info("Connected to Chrome",
			"cdp", cfg.CDPEndpoint,
			"environment", cfg.EnvironmentName,
			"websocket", wsURL,
			"product", version.Product,
			"protocolVersion", version.ProtocolVersion,
		)
	} else {
		logger.Info("Starting without initial Chrome connection; add an environment via browser_connect_environment or browser_switch_environment")
	}

	executor, err := tools.NewExecutor(ctx, cdpClient, cfg.CDPEndpoint, cfg.EnvironmentName, logger, cfg.LowInjection)
	if err != nil {
		return err
	}
	defer executor.Close()

	handler := mcp.NewHandler(router, executor, "brosdk-mcp", buildVersion)

	switch cfg.Mode {
	case config.ModeStdio:
		logger.Info("Chrome MCP Server started (stdio mode)")
		return stdio.NewServer(logger, handler).Run(ctx)

	case config.ModeSSE:
		logger.Info("Chrome MCP Server started (sse mode)")
		srv := sse.NewServer(cfg.EffectivePort(), logger, handler)
		endpoints, err := srv.Start()
		if err != nil {
			return err
		}

		logger.Info("MCP SSE endpoint", "url", endpoints.SSE)
		logger.Info("MCP message endpoint", "url", endpoints.Message)
		logger.Info("MCP UI endpoint", "url", endpoints.UI)

		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)

	default:
		return fmt.Errorf("unsupported mode %q", cfg.Mode)
	}
}
