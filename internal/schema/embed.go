package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed browser-tools.schema.json
var embeddedSchema []byte

// LoadEmbedded reads the bundled schema from the binary's embedded data.
// This is the preferred loading method: it requires no external files and
// makes the binary fully portable.
func LoadEmbedded() (*Registry, error) {
	return LoadFrom(embeddedSchema)
}

// LoadFrom parses a schema from raw JSON bytes. The BOM-prefix stripping
// matches the behavior of Load(path) so callers are interchangeable.
func LoadFrom(raw []byte) (*Registry, error) {
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	var reg Registry
	if err := json.Unmarshal(raw, &reg); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}

	if err := reg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid schema: %w", err)
	}

	return &reg, nil
}
