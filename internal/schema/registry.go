package schema

import (
	"encoding/json"
	"errors"
	"fmt"
)

type Registry struct {
	Version             string         `json:"version"`
	ToolCount           int            `json:"toolCount"`
	Tools               []Tool         `json:"tools"`
	DefaultOutputSchema map[string]any `json:"defaultOutputSchema"`
}

type Tool struct {
	Name         string         `json:"name"`
	Category     string         `json:"category"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
	LegacyNames  []string       `json:"legacyNames,omitempty"`
	Aliases      []string       `json:"aliases,omitempty"`
}

func (r *Registry) Validate() error {
	if r.ToolCount != len(r.Tools) {
		return fmt.Errorf("toolCount mismatch: expected %d, actual %d", r.ToolCount, len(r.Tools))
	}

	seen := make(map[string]struct{}, len(r.Tools))
	for i := range r.Tools {
		tool := &r.Tools[i]
		if tool.Name == "" {
			return fmt.Errorf("tool[%d] has empty name", i)
		}
		if _, exists := seen[tool.Name]; exists {
			return fmt.Errorf("duplicate tool name %q", tool.Name)
		}
		seen[tool.Name] = struct{}{}

		if tool.InputSchema == nil {
			return fmt.Errorf("tool %q has empty inputSchema", tool.Name)
		}
		if tool.OutputSchema == nil {
			if r.DefaultOutputSchema == nil {
				return fmt.Errorf("tool %q has no outputSchema and no defaultOutputSchema", tool.Name)
			}
			clone, err := cloneMap(r.DefaultOutputSchema)
			if err != nil {
				return fmt.Errorf("clone defaultOutputSchema for %q: %w", tool.Name, err)
			}
			tool.OutputSchema = clone
		}
	}

	return nil
}

func cloneMap(src map[string]any) (map[string]any, error) {
	if src == nil {
		return nil, errors.New("nil map")
	}

	raw, err := json.Marshal(src)
	if err != nil {
		return nil, err
	}
	var dst map[string]any
	if err := json.Unmarshal(raw, &dst); err != nil {
		return nil, err
	}
	return dst, nil
}
