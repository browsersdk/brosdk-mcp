package schema

import (
	"testing"
)

func TestRegistryValidateCountMismatch(t *testing.T) {
	reg := &Registry{
		ToolCount: 1,
		Tools: []Tool{
			{
				Name:        "browser_navigate",
				InputSchema: map[string]any{"type": "object"},
			},
			{
				Name:        "browser_click",
				InputSchema: map[string]any{"type": "object"},
			},
		},
		DefaultOutputSchema: map[string]any{"type": "object", "additionalProperties": true},
	}

	if err := reg.Validate(); err == nil {
		t.Fatal("expected count mismatch error")
	}
}

func TestLoadEmbedded(t *testing.T) {
	reg, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded failed: %v", err)
	}

	if reg.ToolCount == 0 || len(reg.Tools) == 0 {
		t.Fatalf("unexpected empty registry: %#v", reg)
	}
}

func TestLoadFromValidJSON(t *testing.T) {
	raw := []byte(`{
		"version": "0.1.0-test",
		"toolCount": 1,
		"tools": [{
			"name": "test_tool",
			"category": "test",
			"description": "test",
			"inputSchema": {"type": "object"},
			"outputSchema": {"type": "object"}
		}],
		"defaultOutputSchema": {"type": "object"}
	}`)

	reg, err := LoadFrom(raw)
	if err != nil {
		t.Fatalf("LoadFrom failed: %v", err)
	}
	if reg.Version != "0.1.0-test" {
		t.Fatalf("expected version 0.1.0-test, got %s", reg.Version)
	}
}

func TestLoadFromInvalidJSON(t *testing.T) {
	raw := []byte(`{not json}`)
	if _, err := LoadFrom(raw); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
