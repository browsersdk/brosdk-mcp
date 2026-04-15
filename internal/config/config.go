package config

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

const (
	ModeStdio = "stdio"
	ModeSSE   = "sse"
)

type Config struct {
	Mode         string
	CDPEndpoint  string
	EnvironmentName string
	Port         int
	LogFile      string
	LowInjection bool
}

func Parse(args []string) (Config, error) {
	fs := flag.NewFlagSet("brosdk-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	mode := fs.String("mode", ModeStdio, "transport mode: stdio or sse")
	cdp := fs.String("cdp", "", "chrome cdp endpoint (host:port or ws://...)")
	name := fs.String("name", "default", "default environment name used when --cdp is provided")
	port := fs.Int("port", 0, "http port for sse mode, <=0 means auto-assign")
	logFile := fs.String("log-file", "", "optional: write logs to this file (in addition to stderr)")
	lowInjection := fs.Bool("low-injection", false, "reduce JS injection by preferring CDP-native actions and disabling JS-heavy fallbacks")

	// --schema is accepted for backwards compatibility but has no effect:
	// the schema is embedded in the binary.
	_ = fs.String("schema", "", "path to tool schema (deprecated: schema is embedded in binary)")

	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}

	cfg := Config{
		Mode:         strings.ToLower(strings.TrimSpace(*mode)),
		CDPEndpoint:  strings.TrimSpace(*cdp),
		EnvironmentName: strings.TrimSpace(*name),
		Port:         *port,
		LogFile:      strings.TrimSpace(*logFile),
		LowInjection: *lowInjection,
	}
	if cfg.EnvironmentName == "" {
		cfg.EnvironmentName = "default"
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	switch c.Mode {
	case ModeStdio, ModeSSE:
	default:
		return fmt.Errorf("invalid --mode %q, expected %q or %q", c.Mode, ModeStdio, ModeSSE)
	}

	if c.Mode == ModeSSE && c.Port < 0 {
		return nil
	}

	return nil
}

func (c Config) EffectivePort() int {
	if c.Mode == ModeSSE && c.Port <= 0 {
		return 0
	}
	return c.Port
}
