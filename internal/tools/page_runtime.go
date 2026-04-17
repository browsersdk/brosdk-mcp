package tools

import "brosdk-mcp/internal/cdp"

type pageRuntime struct {
	TabID        string
	PageClient   *cdp.Client
	AriaRefStore map[string]ariaRefMeta
}

func newPageRuntime(tabID string, pageClient *cdp.Client, ariaRefStore map[string]ariaRefMeta) *pageRuntime {
	return &pageRuntime{
		TabID:        tabID,
		PageClient:   pageClient,
		AriaRefStore: cloneAriaRefMetaMap(ariaRefStore),
	}
}

func clonePageRuntime(src *pageRuntime) *pageRuntime {
	if src == nil {
		return nil
	}
	return &pageRuntime{
		TabID:        src.TabID,
		PageClient:   src.PageClient,
		AriaRefStore: cloneAriaRefMetaMap(src.AriaRefStore),
	}
}

func cloneAriaRefMetaMap(src map[string]ariaRefMeta) map[string]ariaRefMeta {
	if src == nil {
		return make(map[string]ariaRefMeta)
	}
	out := make(map[string]ariaRefMeta, len(src))
	for ref, meta := range src {
		out[ref] = meta
	}
	return out
}

func cloneAriaRefStoreForTab(src map[string]map[string]ariaRefMeta, tabID string) map[string]ariaRefMeta {
	if src == nil || tabID == "" {
		return make(map[string]ariaRefMeta)
	}
	return cloneAriaRefMetaMap(src[tabID])
}

func exportPageRuntimesToAriaRefStore(pages map[string]*pageRuntime) map[string]map[string]ariaRefMeta {
	if pages == nil {
		return make(map[string]map[string]ariaRefMeta)
	}
	out := make(map[string]map[string]ariaRefMeta, len(pages))
	for tabID, page := range pages {
		if tabID == "" || page == nil {
			continue
		}
		out[tabID] = cloneAriaRefMetaMap(page.AriaRefStore)
	}
	return out
}
