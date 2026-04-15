package tools

import "strings"

func (e *Executor) storeAriaRefMeta(tabID string, meta map[string]map[string]any) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return
	}

	converted := make(map[string]ariaRefMeta, len(meta))
	for ref, raw := range meta {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		converted[ref] = parseAriaRefMeta(raw)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ariaRefStore == nil {
		e.ariaRefStore = make(map[string]map[string]ariaRefMeta)
	}
	e.ariaRefStore[tabID] = converted
}

func (e *Executor) getStoredAriaRefMeta(tabID string, ref string) (*ariaRefMeta, bool) {
	tabID = strings.TrimSpace(tabID)
	ref = strings.TrimSpace(ref)
	if tabID == "" || ref == "" {
		return nil, false
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	tabMeta, ok := e.ariaRefStore[tabID]
	if !ok {
		return nil, false
	}
	meta, ok := tabMeta[ref]
	if !ok {
		return nil, false
	}
	copy := meta
	return &copy, true
}

func (e *Executor) clearStoredAriaRefMeta(tabID string) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.ariaRefStore, tabID)
}

func parseAriaRefMeta(raw map[string]any) ariaRefMeta {
	meta := ariaRefMeta{
		Role:    strings.TrimSpace(asString(raw["role"])),
		Name:    strings.TrimSpace(asString(raw["name"])),
		Nth:     asInt(raw["nth"]),
		FrameID: strings.TrimSpace(asString(raw["frameId"])),
	}

	backend := asInt64(raw["backendNodeId"])
	if backend < 0 {
		backend = 0
	}
	meta.BackendNodeID = backend
	return meta
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return ""
	}
}

func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int32:
		return int(x)
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
}

func asInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	default:
		return 0
	}
}
