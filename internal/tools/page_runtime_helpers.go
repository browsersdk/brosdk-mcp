package tools

import (
	"context"
	"fmt"
	"strings"
)

func (e *Executor) activeEnvironmentLocked() *browserEnvironment {
	if e.environments == nil {
		return nil
	}
	return e.environments[strings.TrimSpace(e.activeEnvironment)]
}

func (e *Executor) currentPageRuntimeLocked() *pageRuntime {
	env := e.activeEnvironmentLocked()
	if env == nil {
		return nil
	}
	tabID := strings.TrimSpace(e.currentTabID)
	if tabID == "" {
		tabID = strings.TrimSpace(env.ActivePageID)
	}
	if tabID == "" {
		return nil
	}
	return env.Pages[tabID]
}

func (e *Executor) ensurePageRuntimeLocked(tabID string) *pageRuntime {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return nil
	}
	env := e.activeEnvironmentLocked()
	if env == nil {
		return nil
	}
	if env.Pages == nil {
		env.Pages = make(map[string]*pageRuntime)
	}
	page := env.Pages[tabID]
	if page == nil {
		page = newPageRuntime(tabID, nil, cloneAriaRefStoreForTab(e.ariaRefStore, tabID))
		env.Pages[tabID] = page
	}
	return page
}

func (e *Executor) syncExecutorStateToCurrentPageLocked() {
	page := e.ensurePageRuntimeLocked(e.currentTabID)
	if page == nil {
		return
	}
	page.TabID = e.currentTabID
	page.PageClient = e.pageClient
	page.AriaRefStore = cloneAriaRefStoreForTab(e.ariaRefStore, e.currentTabID)

	env := e.activeEnvironmentLocked()
	if env != nil {
		env.ActivePageID = e.currentTabID
	}
}

func (e *Executor) loadPageRuntimeIntoExecutorLocked(tabID string) {
	tabID = strings.TrimSpace(tabID)
	if tabID == "" {
		return
	}
	env := e.activeEnvironmentLocked()
	if env == nil {
		return
	}
	page := env.Pages[tabID]
	if page == nil {
		return
	}
	e.currentTabID = page.TabID
	e.pageClient = page.PageClient
	if e.ariaRefStore == nil {
		e.ariaRefStore = make(map[string]map[string]ariaRefMeta)
	}
	e.ariaRefStore[tabID] = cloneAriaRefMetaMap(page.AriaRefStore)
	env.ActivePageID = tabID
}

func (e *Executor) ensureActivePageRuntime(ctx context.Context) (*pageRuntime, error) {
	e.mu.Lock()
	if page := clonePageRuntime(e.currentPageRuntimeLocked()); page != nil {
		e.mu.Unlock()
		return page, nil
	}
	e.mu.Unlock()

	pageClient, tabID, err := e.getCurrentPageClient(ctx)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	page := e.ensurePageRuntimeLocked(tabID)
	if page == nil {
		return nil, fmt.Errorf("page runtime unavailable for tab %s", tabID)
	}
	page.PageClient = pageClient
	page.TabID = tabID
	page.AriaRefStore = cloneAriaRefStoreForTab(e.ariaRefStore, tabID)
	return clonePageRuntime(page), nil
}
