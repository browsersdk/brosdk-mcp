package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"brosdk-mcp/internal/cdp"
)

const defaultEnvironmentName = "default"

type browserEnvironment struct {
	Name           string
	CDPEndpoint    string
	BrowserClient  *cdp.Client
	OwnsBrowser    bool
	BrowserProcess *exec.Cmd
	UserDataDir    string

	CurrentTabID string
	PageClient   *cdp.Client
	AriaRefStore map[string]map[string]ariaRefMeta
	Connected    bool
}

func newBrowserEnvironment(name string, cdpEndpoint string, browserClient *cdp.Client, ownsBrowser bool) *browserEnvironment {
	return &browserEnvironment{
		Name:          strings.TrimSpace(name),
		CDPEndpoint:   strings.TrimSpace(cdpEndpoint),
		BrowserClient: browserClient,
		OwnsBrowser:   ownsBrowser,
		AriaRefStore:  make(map[string]map[string]ariaRefMeta),
		Connected:     browserClient != nil,
	}
}

func (e *Executor) initializeEnvironmentState(defaultName string) {
	e.environments = make(map[string]*browserEnvironment)
	defaultName = strings.TrimSpace(defaultName)
	if defaultName == "" {
		defaultName = defaultEnvironmentName
	}
	if e.browserClient == nil {
		e.activeEnvironment = ""
		return
	}
	env := newBrowserEnvironment(defaultName, e.cdpEndpoint, e.browserClient, false)
	env.CurrentTabID = e.currentTabID
	env.PageClient = e.pageClient
	env.AriaRefStore = cloneAriaRefStore(e.ariaRefStore)
	env.Connected = e.browserClient != nil
	e.environments[defaultName] = env
	e.activeEnvironment = defaultName
}

func cloneAriaRefStore(src map[string]map[string]ariaRefMeta) map[string]map[string]ariaRefMeta {
	if src == nil {
		return make(map[string]map[string]ariaRefMeta)
	}
	out := make(map[string]map[string]ariaRefMeta, len(src))
	for tabID, meta := range src {
		copied := make(map[string]ariaRefMeta, len(meta))
		for ref, m := range meta {
			copied[ref] = m
		}
		out[tabID] = copied
	}
	return out
}

func (e *Executor) snapshotActiveEnvironmentLocked() {
	if e.environments == nil || strings.TrimSpace(e.activeEnvironment) == "" {
		return
	}
	env, ok := e.environments[e.activeEnvironment]
	if !ok || env == nil {
		return
	}
	env.BrowserClient = e.browserClient
	env.CDPEndpoint = e.cdpEndpoint
	env.CurrentTabID = e.currentTabID
	env.PageClient = e.pageClient
	env.AriaRefStore = cloneAriaRefStore(e.ariaRefStore)
	env.Connected = e.browserClient != nil
}

func (e *Executor) loadEnvironmentLocked(name string) error {
	if e.environments == nil {
		return fmt.Errorf("environment manager not initialized")
	}
	env, ok := e.environments[strings.TrimSpace(name)]
	if !ok || env == nil {
		return e.environmentNotFoundErrorLocked(strings.TrimSpace(name))
	}
	e.browserClient = env.BrowserClient
	e.cdpEndpoint = env.CDPEndpoint
	e.currentTabID = env.CurrentTabID
	e.pageClient = env.PageClient
	e.ariaRefStore = cloneAriaRefStore(env.AriaRefStore)
	e.activeEnvironment = env.Name
	return nil
}

func (e *Executor) switchActiveEnvironment(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.snapshotActiveEnvironmentLocked()
	return e.loadEnvironmentLocked(name)
}

func (e *Executor) activeEnvironmentName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.TrimSpace(e.activeEnvironment)
}

func (e *Executor) withEnvironment(args map[string]any, fn func() (map[string]any, error)) (map[string]any, error) {
	targetName, ok := getStringArg(args, "environment")
	if !ok || strings.TrimSpace(targetName) == "" {
		return fn()
	}
	targetName = strings.TrimSpace(targetName)

	e.mu.Lock()
	originalName := strings.TrimSpace(e.activeEnvironment)
	if originalName == targetName {
		e.mu.Unlock()
		return fn()
	}
	e.snapshotActiveEnvironmentLocked()
	if err := e.loadEnvironmentLocked(targetName); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	e.mu.Unlock()

	defer func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.snapshotActiveEnvironmentLocked()
		if originalName != "" {
			_ = e.loadEnvironmentLocked(originalName)
		}
	}()

	return fn()
}

func (e *Executor) listEnvironmentNamesLocked() []string {
	names := make([]string, 0, len(e.environments))
	for name := range e.environments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (e *Executor) allocateEnvironmentNameLocked(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "local"
	}
	if _, exists := e.environments[prefix]; !exists {
		return prefix
	}
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d", prefix, i)
		if _, exists := e.environments[candidate]; !exists {
			return candidate
		}
	}
}

func (e *Executor) environmentNotFoundError(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.environmentNotFoundErrorLocked(name)
}

func (e *Executor) environmentNotFoundErrorLocked(name string) error {
	names := e.listEnvironmentNamesLocked()
	if len(names) == 0 {
		return fmt.Errorf("environment %q not found", name)
	}
	return fmt.Errorf("environment %q not found; available environments: %s", name, strings.Join(names, ", "))
}

func (e *Executor) connectBrowserEnvironment(ctx context.Context, env *browserEnvironment) error {
	if env == nil || env.BrowserClient == nil {
		return fmt.Errorf("environment has no browser connection")
	}
	tabID, err := ensureInitialTabForBrowser(ctx, env.BrowserClient)
	if err != nil {
		return err
	}
	pageClient, err := env.BrowserClient.AttachToTarget(ctx, tabID)
	if err != nil {
		return err
	}
	if err := enablePageSession(ctx, pageClient); err != nil {
		_ = pageClient.Close()
		return err
	}
	if env.PageClient != nil {
		_ = env.PageClient.Close()
	}
	env.PageClient = pageClient
	env.CurrentTabID = tabID
	env.Connected = true
	if env.AriaRefStore == nil {
		env.AriaRefStore = make(map[string]map[string]ariaRefMeta)
	}
	return nil
}

func ensureInitialTabForBrowser(ctx context.Context, browserClient *cdp.Client) (string, error) {
	targets, err := listPageTargetsForBrowser(ctx, browserClient)
	if err != nil {
		return "", err
	}
	if len(targets) > 0 {
		return targets[0].ID, nil
	}
	targetID, err := browserClient.CreateTarget(ctx, "about:blank")
	if err != nil {
		return "", fmt.Errorf("create initial target: %w", err)
	}
	return targetID, nil
}

func listPageTargetsForBrowser(ctx context.Context, browserClient *cdp.Client) ([]cdp.Target, error) {
	targets, err := browserClient.GetTargets(ctx)
	if err != nil {
		return nil, fmt.Errorf("Target.getTargets failed: %w", err)
	}
	pages := make([]cdp.Target, 0, len(targets))
	for _, t := range targets {
		if t.Type == "page" {
			pages = append(pages, t)
		}
	}
	return pages, nil
}

func (e *Executor) addEnvironment(ctx context.Context, name string, cdpEndpoint string, setActive bool) (*browserEnvironment, error) {
	name = strings.TrimSpace(name)
	cdpEndpoint = strings.TrimSpace(cdpEndpoint)
	if cdpEndpoint == "" {
		return nil, fmt.Errorf("missing required argument cdp_endpoint")
	}

	e.mu.Lock()
	if e.environments == nil {
		e.environments = make(map[string]*browserEnvironment)
	}
	if name == "" {
		name = e.allocateEnvironmentNameLocked("environment")
	}
	if _, exists := e.environments[name]; exists {
		e.mu.Unlock()
		return nil, fmt.Errorf("environment %q already exists", name)
	}
	e.snapshotActiveEnvironmentLocked()
	e.mu.Unlock()

	wsURL, err := cdp.DiscoverWebSocketURL(ctx, cdpEndpoint)
	if err != nil {
		return nil, err
	}
	browserClient, err := cdp.NewClient(ctx, wsURL, e.logger)
	if err != nil {
		return nil, err
	}

	env := newBrowserEnvironment(name, cdpEndpoint, browserClient, true)
	if err := e.connectBrowserEnvironment(ctx, env); err != nil {
		_ = browserClient.Close()
		return nil, err
	}

	e.mu.Lock()
	e.environments[name] = env
	if setActive {
		if err := e.loadEnvironmentLocked(name); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	} else if active := strings.TrimSpace(e.activeEnvironment); active != "" {
		_ = e.loadEnvironmentLocked(active)
	}
	e.mu.Unlock()
	return env, nil
}

func closeManagedBrowserProcess(env *browserEnvironment) {
	if env == nil {
		return
	}
	if env.PageClient != nil {
		_ = env.PageClient.Close()
		env.PageClient = nil
	}
	if env.OwnsBrowser && env.BrowserClient != nil {
		_ = env.BrowserClient.Close()
		env.BrowserClient = nil
	}
	if env.BrowserProcess != nil && env.BrowserProcess.Process != nil {
		_ = env.BrowserProcess.Process.Kill()
		_, _ = env.BrowserProcess.Process.Wait()
		env.BrowserProcess = nil
	}
	if env.UserDataDir != "" {
		_ = os.RemoveAll(env.UserDataDir)
		env.UserDataDir = ""
	}
	env.Connected = false
}

func (e *Executor) launchLocalEnvironment(ctx context.Context, name string, executablePath string, initialURL string, headless bool, setActive bool) (*browserEnvironment, error) {
	name = strings.TrimSpace(name)
	executablePath = strings.TrimSpace(executablePath)
	initialURL = strings.TrimSpace(initialURL)
	if initialURL == "" {
		initialURL = "about:blank"
	}
	if executablePath == "" {
		var ok bool
		executablePath, ok = findLocalChromeExecutable()
		if !ok {
			return nil, fmt.Errorf("chrome executable not found; set executable_path or configure BROSDK_CHROME_PATH/CHROME_PATH")
		}
	}

	e.mu.Lock()
	if e.environments == nil {
		e.environments = make(map[string]*browserEnvironment)
	}
	if name == "" {
		name = e.allocateEnvironmentNameLocked("local")
	}
	if _, exists := e.environments[name]; exists {
		e.mu.Unlock()
		return nil, fmt.Errorf("environment %q already exists", name)
	}
	e.snapshotActiveEnvironmentLocked()
	e.mu.Unlock()

	userDataDir, err := os.MkdirTemp("", "brosdk-mcp-local-*")
	if err != nil {
		return nil, fmt.Errorf("create temp user-data-dir: %w", err)
	}

	cmd, debugPort, err := startChromeWithDynamicDebugPort(ctx, executablePath, userDataDir, initialURL, headless)
	if err != nil {
		_ = os.RemoveAll(userDataDir)
		return nil, err
	}

	cdpEndpoint := fmt.Sprintf("127.0.0.1:%d", debugPort)
	wsURL, err := cdp.DiscoverWebSocketURL(ctx, cdpEndpoint)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.RemoveAll(userDataDir)
		return nil, err
	}
	browserClient, err := cdp.NewClient(ctx, wsURL, e.logger)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = os.RemoveAll(userDataDir)
		return nil, err
	}

	env := newBrowserEnvironment(name, cdpEndpoint, browserClient, true)
	env.BrowserProcess = cmd
	env.UserDataDir = userDataDir
	if err := e.connectBrowserEnvironment(ctx, env); err != nil {
		closeManagedBrowserProcess(env)
		return nil, err
	}

	e.mu.Lock()
	e.environments[name] = env
	if setActive {
		if err := e.loadEnvironmentLocked(name); err != nil {
			e.mu.Unlock()
			closeManagedBrowserProcess(env)
			return nil, err
		}
	} else if active := strings.TrimSpace(e.activeEnvironment); active != "" {
		_ = e.loadEnvironmentLocked(active)
	}
	e.mu.Unlock()
	return env, nil
}

func (e *Executor) closeEnvironment(name string) (*browserEnvironment, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", fmt.Errorf("missing required argument name")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.snapshotActiveEnvironmentLocked()

	env, ok := e.environments[name]
	if !ok || env == nil {
		return nil, "", e.environmentNotFoundErrorLocked(name)
	}
	delete(e.environments, name)

	closeManagedBrowserProcess(env)

	nextActive := strings.TrimSpace(e.activeEnvironment)
	if nextActive == name {
		nextActive = ""
		names := e.listEnvironmentNamesLocked()
		if len(names) > 0 {
			nextActive = names[0]
			_ = e.loadEnvironmentLocked(nextActive)
		} else {
			e.activeEnvironment = ""
			e.browserClient = nil
			e.cdpEndpoint = ""
			e.currentTabID = ""
			e.pageClient = nil
			e.ariaRefStore = make(map[string]map[string]ariaRefMeta)
		}
	}
	return env, nextActive, nil
}

func (e *Executor) listEnvironments() []map[string]any {
	e.mu.Lock()
	e.snapshotActiveEnvironmentLocked()
	names := e.listEnvironmentNamesLocked()
	envs := make([]*browserEnvironment, 0, len(names))
	activeName := e.activeEnvironment
	for _, name := range names {
		envs = append(envs, e.environments[name])
	}
	e.mu.Unlock()

	result := make([]map[string]any, 0, len(names))
	for idx, name := range names {
		env := envs[idx]
		tabCount := 0
		if env != nil && env.BrowserClient != nil {
			if targets, err := listPageTargetsForBrowser(context.Background(), env.BrowserClient); err == nil {
				tabCount = len(targets)
			}
		}
		status := "disconnected"
		if env != nil && env.Connected && env.BrowserClient != nil {
			status = "connected"
		}
		cdpEndpoint := ""
		if env != nil {
			cdpEndpoint = env.CDPEndpoint
		}
		result = append(result, map[string]any{
			"name":        name,
			"active":      name == activeName,
			"status":      status,
			"tabCount":    tabCount,
			"cdpEndpoint": cdpEndpoint,
		})
	}
	return result
}

func (e *Executor) callAddEnvironment(ctx context.Context, args map[string]any) (map[string]any, error) {
	name, _ := getStringArg(args, "name")
	cdpEndpoint, _ := getStringArg(args, "cdp_endpoint")
	setActive := true
	if v, ok, err := getBoolArg(args, "set_active"); err != nil {
		return nil, err
	} else if ok {
		setActive = v
	}
	env, err := e.addEnvironment(ctx, name, cdpEndpoint, setActive)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "name": env.Name, "active": setActive, "status": "connected", "cdpEndpoint": env.CDPEndpoint}, nil
}

func (e *Executor) callLaunchLocalEnvironment(ctx context.Context, args map[string]any) (map[string]any, error) {
	name, _ := getStringArg(args, "name")
	executablePath, _ := getStringArg(args, "executable_path")
	initialURL, _ := getStringArg(args, "initial_url")
	headless := true
	if v, ok, err := getBoolArg(args, "headless"); err != nil {
		return nil, err
	} else if ok {
		headless = v
	}
	setActive := true
	if v, ok, err := getBoolArg(args, "set_active"); err != nil {
		return nil, err
	} else if ok {
		setActive = v
	}

	env, err := e.launchLocalEnvironment(ctx, name, executablePath, initialURL, headless, setActive)
	if err != nil {
		return nil, err
	}
	pid := 0
	if env.BrowserProcess != nil && env.BrowserProcess.Process != nil {
		pid = env.BrowserProcess.Process.Pid
	}
	return map[string]any{
		"ok":          true,
		"name":        env.Name,
		"active":      setActive,
		"status":      "connected",
		"cdpEndpoint": env.CDPEndpoint,
		"pid":         pid,
		"userDataDir": env.UserDataDir,
	}, nil
}

func (e *Executor) callListEnvironments(ctx context.Context, args map[string]any) (map[string]any, error) {
	_ = ctx
	_ = args
	return map[string]any{
		"activeEnvironment": e.activeEnvironmentName(),
		"environments":      e.listEnvironments(),
	}, nil
}

func (e *Executor) callUseEnvironment(ctx context.Context, args map[string]any) (map[string]any, error) {
	name, _ := getStringArg(args, "name")
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("missing required argument name")
	}
	cdpEndpoint, _ := getStringArg(args, "cdp_endpoint")

	e.mu.Lock()
	_, exists := e.environments[name]
	e.mu.Unlock()

	if !exists {
		if strings.TrimSpace(cdpEndpoint) == "" {
			return nil, e.environmentNotFoundError(name)
		}
		env, err := e.addEnvironment(ctx, name, cdpEndpoint, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "name": env.Name, "active": true, "created": true}, nil
	}

	if err := e.switchActiveEnvironment(name); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "name": name, "active": true, "created": false}, nil
}

func (e *Executor) callCloseEnvironment(ctx context.Context, args map[string]any) (map[string]any, error) {
	_ = ctx
	name, _ := getStringArg(args, "name")
	env, nextActive, err := e.closeEnvironment(name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "closed": env.Name, "activeEnvironment": nextActive}, nil
}

func startChromeWithDynamicDebugPort(ctx context.Context, chromePath string, userDataDir string, initialURL string, headless bool) (*exec.Cmd, int, error) {
	args := []string{
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--remote-debugging-port=0",
	}
	if headless {
		args = append(args, "--headless=new")
	}
	args = append(args, initialURL)

	cmd := exec.CommandContext(ctx, chromePath, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}

	portFile := filepath.Join(userDataDir, "DevToolsActivePort")
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(portFile)
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) >= 1 {
				port, err := strconv.Atoi(strings.TrimSpace(lines[0]))
				if err == nil && port > 0 {
					return cmd, port, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			return nil, 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}

	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	return nil, 0, fmt.Errorf("DevToolsActivePort not ready in %s", userDataDir)
}

func findLocalChromeExecutable() (string, bool) {
	if v := strings.TrimSpace(os.Getenv("BROSDK_CHROME_PATH")); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, true
		}
	}
	if v := strings.TrimSpace(os.Getenv("CHROME_PATH")); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v, true
		}
	}

	candidates := []string{
		"chrome",
		"chrome.exe",
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
	}
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, true
		}
	}

	winCandidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
	}
	for _, p := range winCandidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}

	return "", false
}
