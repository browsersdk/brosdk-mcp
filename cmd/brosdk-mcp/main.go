package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"brosdk-mcp/internal/app"
	"brosdk-mcp/internal/config"
)

// Version is injected at build time via -ldflags.
// Example: go build -ldflags "-X main.Version=v0.2.0" ./cmd/brosdk-mcp
var Version = "dev"

func main() {
	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	var logger *slog.Logger
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			slog.Error("cannot open log file", "path", cfg.LogFile, "error", err)
			os.Exit(2)
		}
		defer f.Close()

		out := io.Writer(f)
		if cfg.Mode != config.ModeStdio {
			out = io.MultiWriter(os.Stderr, f)
		}

		fileHandler := slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelInfo})
		logger = slog.New(fileHandler)
	} else {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := app.Run(ctx, cfg, logger, Version); err != nil {
		logger.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}
