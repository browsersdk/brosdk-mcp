package config

import "testing"

func TestParseValidSSE(t *testing.T) {
	cfg, err := Parse([]string{"--mode", "sse", "--cdp", "localhost:9222", "--port", "-1"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	if cfg.Mode != ModeSSE {
		t.Fatalf("unexpected mode: %s", cfg.Mode)
	}

	if cfg.EffectivePort() != 0 {
		t.Fatalf("expected effective port 0, got %d", cfg.EffectivePort())
	}
	if cfg.EnvironmentName != "default" {
		t.Fatalf("expected default environment name, got %q", cfg.EnvironmentName)
	}
}

func TestParseAllowsMissingCDP(t *testing.T) {
	cfg, err := Parse([]string{"--mode", "stdio"})
	if err != nil {
		t.Fatalf("expected missing --cdp to be allowed, got %v", err)
	}
	if cfg.CDPEndpoint != "" {
		t.Fatalf("expected empty cdp endpoint, got %q", cfg.CDPEndpoint)
	}
}

func TestParseInvalidMode(t *testing.T) {
	_, err := Parse([]string{"--mode", "grpc", "--cdp", "localhost:9222"})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestParseLowInjection(t *testing.T) {
	cfg, err := Parse([]string{"--mode", "stdio", "--cdp", "localhost:9222", "--low-injection"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if !cfg.LowInjection {
		t.Fatal("expected LowInjection=true")
	}
}

func TestParseEnvironmentName(t *testing.T) {
	cfg, err := Parse([]string{"--mode", "stdio", "--cdp", "localhost:9222", "--name", "work"})
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if cfg.EnvironmentName != "work" {
		t.Fatalf("expected environment name work, got %q", cfg.EnvironmentName)
	}
}
