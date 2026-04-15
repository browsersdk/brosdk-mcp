package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"brosdk-mcp/internal/cdp"
)

const (
	maxAriaSnapshotLines = 500
	maxAriaSnapshotRefs  = 500
)

type axValue struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

type axNode struct {
	NodeID           string   `json:"nodeId"`
	Ignored          bool     `json:"ignored"`
	Role             *axValue `json:"role"`
	Name             *axValue `json:"name"`
	Value            *axValue `json:"value"`
	ChildIDs         []string `json:"childIds"`
	BackendDOMNodeID int64    `json:"backendDOMNodeId"`
	// FrameID is set on nodes that represent an embedded frame (iframe/fencedframe).
	// When non-empty the AX tree for that frame must be fetched via a separate session.
	FrameID string `json:"frameId"`
}

type axTreeResponse struct {
	Nodes []axNode `json:"nodes"`
}

// tryBuildAXSnapshot fetches the full AX tree for the current page (and any
// embedded iframes) and returns a text snapshot together with ref metadata.
//
// Cross-frame strategy:
//  1. Fetch the top-level AX tree with pierce=true (covers shadow DOM).
//  2. Walk the resulting nodes; any node whose FrameID is non-empty represents
//     an iframe whose subtree was NOT included by the top-level call.
//  3. For each such frame, attach a temporary CDP session, fetch its AX tree,
//     and splice the nodes into the parent tree by replacing the placeholder
//     node's ChildIDs with the frame's root node IDs.
func (e *Executor) tryBuildAXSnapshot(ctx context.Context, pageClient *cdp.Client, interactiveOnly bool, compact bool, maxDepth int) (string, map[string]map[string]any, error) {
	if pageClient == nil {
		return "", nil, fmt.Errorf("nil page client")
	}

	_, _ = e.callPageClient(ctx, pageClient, "Accessibility.enable", nil)
	raw, err := e.callPageClient(ctx, pageClient, "Accessibility.getFullAXTree", map[string]any{
		"pierce": true,
	})
	if err != nil {
		return "", nil, err
	}

	var payload axTreeResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", nil, fmt.Errorf("decode Accessibility.getFullAXTree response: %w", err)
	}
	if len(payload.Nodes) == 0 {
		return "", nil, fmt.Errorf("empty accessibility tree")
	}

	// Merge iframe subtrees into the flat node list.
	payload.Nodes = e.mergeIframeAXNodes(ctx, pageClient, payload.Nodes)

	title, _ := e.evaluateString(ctx, pageClient, `(function(){ return document.title || ""; })()`)
	snapshot, meta := buildAXSnapshot(payload.Nodes, interactiveOnly, compact, maxDepth, title)
	if strings.TrimSpace(snapshot) == "" {
		return "", nil, fmt.Errorf("empty accessibility snapshot")
	}
	if len(meta) == 0 {
		return "", nil, fmt.Errorf("no actionable refs in accessibility snapshot")
	}
	return snapshot, meta, nil
}

// mergeIframeAXNodes walks the flat node list, detects iframe placeholder nodes
// (FrameID != ""), fetches their AX subtrees via temporary frame sessions, and
// splices the results in-place.  Errors per-frame are silently skipped so that
// a sandboxed or cross-origin iframe does not abort the whole snapshot.
func (e *Executor) mergeIframeAXNodes(ctx context.Context, pageClient *cdp.Client, nodes []axNode) []axNode {
	// Collect unique frame IDs that need expansion.
	seen := make(map[string]bool)
	for _, n := range nodes {
		fid := strings.TrimSpace(n.FrameID)
		if fid != "" {
			seen[fid] = true
		}
	}
	for _, fid := range e.childFrameIDs(ctx, pageClient) {
		if strings.TrimSpace(fid) != "" {
			seen[strings.TrimSpace(fid)] = true
		}
	}

	for fid := range seen {
		frameNodes, err := e.fetchFrameAXNodes(ctx, pageClient, fid)
		if err != nil {
			if e.logger != nil {
				e.logger.Debug("ax iframe merge: skip frame", "frameId", fid, "error", err)
			}
			continue
		}
		if len(frameNodes) == 0 {
			continue
		}
		frameNodes = annotateAXFrameID(frameNodes, fid)

		// Namespace frame node IDs to avoid collisions with the parent tree.
		prefix := "frame:" + fid + ":"
		frameNodes = namespaceAXNodeIDs(frameNodes, prefix)

		// Find the placeholder node(s) for this frame and replace their
		// ChildIDs with the frame's root node IDs.
		frameRoots := axRootNodeIDs(frameNodes)
		for i, n := range nodes {
			if strings.TrimSpace(n.FrameID) == fid {
				nodes[i].ChildIDs = frameRoots
			}
		}

		nodes = append(nodes, frameNodes...)
	}
	return nodes
}

func (e *Executor) childFrameIDs(ctx context.Context, pageClient *cdp.Client) []string {
	if pageClient == nil {
		return nil
	}

	raw, err := e.callPageClient(ctx, pageClient, "Page.getFrameTree", nil)
	if err != nil {
		return nil
	}

	var payload struct {
		FrameTree frameTreeNode `json:"frameTree"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}

	var result []string
	var walk func(node frameTreeNode, depth int)
	walk = func(node frameTreeNode, depth int) {
		if depth > 0 && strings.TrimSpace(node.Frame.ID) != "" {
			result = append(result, strings.TrimSpace(node.Frame.ID))
		}
		for _, child := range node.ChildFrames {
			walk(child, depth+1)
		}
	}
	walk(payload.FrameTree, 0)
	return result
}

type frameTreeNode struct {
	Frame struct {
		ID string `json:"id"`
	} `json:"frame"`
	ChildFrames []frameTreeNode `json:"childFrames"`
}

// fetchFrameAXNodes attaches a temporary session to the frame identified by
// frameID and returns its flat AX node list.
func (e *Executor) fetchFrameAXNodes(ctx context.Context, pageClient *cdp.Client, frameID string) ([]axNode, error) {
	if pageClient == nil {
		return nil, fmt.Errorf("nil page client")
	}

	// Approach A: use Accessibility.getFullAXTree scoped to the frameId via
	// the existing page session (works for same-process iframes in Chrome ≥ 112).
	rawA, errA := pageClient.Call(ctx, "Accessibility.getFullAXTree", map[string]any{
		"pierce":  true,
		"frameId": frameID,
	})
	if errA == nil {
		var payload axTreeResponse
		if jsonErr := json.Unmarshal(rawA, &payload); jsonErr == nil && len(payload.Nodes) > 0 {
			return payload.Nodes, nil
		}
	}

	// Approach B: find the iframe as a separate CDP target and attach to it.
	// Chrome uses targetId == frameId for out-of-process iframes.
	targetsRaw, errB := pageClient.Call(ctx, "Target.getTargets", nil)
	if errB != nil {
		return nil, fmt.Errorf("Target.getTargets: %w (approach A: %v)", errB, errA)
	}
	var targetsPayload struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(targetsRaw, &targetsPayload); err != nil {
		return nil, fmt.Errorf("decode Target.getTargets: %w", err)
	}

	for _, t := range targetsPayload.TargetInfos {
		if t.TargetID != frameID {
			continue
		}
		frameClient, attachErr := pageClient.AttachToTarget(ctx, t.TargetID)
		if attachErr != nil {
			return nil, fmt.Errorf("attach to iframe target %s: %w", t.TargetID, attachErr)
		}
		defer frameClient.Close()

		_, _ = frameClient.Call(ctx, "Accessibility.enable", nil)
		frameRaw, callErr := frameClient.Call(ctx, "Accessibility.getFullAXTree", map[string]any{
			"pierce": true,
		})
		if callErr != nil {
			return nil, fmt.Errorf("getFullAXTree for frame %s: %w", frameID, callErr)
		}
		var fp axTreeResponse
		if jsonErr := json.Unmarshal(frameRaw, &fp); jsonErr != nil {
			return nil, fmt.Errorf("decode frame AX tree: %w", jsonErr)
		}
		return fp.Nodes, nil
	}

	return nil, fmt.Errorf("no matching target for frameId %s (approach A: %v)", frameID, errA)
}

// namespaceAXNodeIDs prefixes all NodeID and ChildIDs values in a node list
// to prevent collisions when merging multiple frame trees.
func namespaceAXNodeIDs(nodes []axNode, prefix string) []axNode {
	result := make([]axNode, len(nodes))
	for i, n := range nodes {
		n.NodeID = prefix + strings.TrimSpace(n.NodeID)
		children := make([]string, len(n.ChildIDs))
		for j, c := range n.ChildIDs {
			children[j] = prefix + strings.TrimSpace(c)
		}
		n.ChildIDs = children
		result[i] = n
	}
	return result
}

func annotateAXFrameID(nodes []axNode, frameID string) []axNode {
	frameID = strings.TrimSpace(frameID)
	if frameID == "" {
		return nodes
	}
	result := make([]axNode, len(nodes))
	for i, n := range nodes {
		n.FrameID = frameID
		result[i] = n
	}
	return result
}

// axRootNodeIDs returns the IDs of nodes that have no parent within the list.
func axRootNodeIDs(nodes []axNode) []string {
	hasParent := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		for _, c := range n.ChildIDs {
			hasParent[strings.TrimSpace(c)] = true
		}
	}
	var roots []string
	for _, n := range nodes {
		id := strings.TrimSpace(n.NodeID)
		if id != "" && !hasParent[id] {
			roots = append(roots, id)
		}
	}
	return roots
}

// tryBuildAXSnapshotForSelector fetches the AX subtree rooted at the element
// matched by selector.  It resolves the element's backendNodeId via DOM.querySelector
// then calls Accessibility.queryAXTree to get only the relevant subtree.
func (e *Executor) tryBuildAXSnapshotForSelector(ctx context.Context, pageClient *cdp.Client, selector string, interactiveOnly bool, compact bool, maxDepth int) (string, map[string]map[string]any, error) {
	if pageClient == nil {
		return "", nil, fmt.Errorf("nil page client")
	}

	// Resolve document node.
	docRaw, err := e.callPageClient(ctx, pageClient, "DOM.getDocument", map[string]any{"depth": 0})
	if err != nil {
		return "", nil, fmt.Errorf("DOM.getDocument: %w", err)
	}
	var docPayload struct {
		Root struct {
			NodeID int64 `json:"nodeId"`
		} `json:"root"`
	}
	if err := json.Unmarshal(docRaw, &docPayload); err != nil {
		return "", nil, fmt.Errorf("decode DOM.getDocument: %w", err)
	}

	// Find the element matching selector.
	qsRaw, err := e.callPageClient(ctx, pageClient, "DOM.querySelector", map[string]any{
		"nodeId":   docPayload.Root.NodeID,
		"selector": selector,
	})
	if err != nil {
		return "", nil, fmt.Errorf("DOM.querySelector(%q): %w", selector, err)
	}
	var qsPayload struct {
		NodeID int64 `json:"nodeId"`
	}
	if err := json.Unmarshal(qsRaw, &qsPayload); err != nil || qsPayload.NodeID == 0 {
		return "", nil, fmt.Errorf("selector %q not found in DOM", selector)
	}

	// Resolve to backendNodeId.
	descRaw, err := e.callPageClient(ctx, pageClient, "DOM.describeNode", map[string]any{
		"nodeId": qsPayload.NodeID,
	})
	if err != nil {
		return "", nil, fmt.Errorf("DOM.describeNode: %w", err)
	}
	var descPayload struct {
		Node struct {
			BackendNodeID int64 `json:"backendNodeId"`
		} `json:"node"`
	}
	if err := json.Unmarshal(descRaw, &descPayload); err != nil || descPayload.Node.BackendNodeID == 0 {
		return "", nil, fmt.Errorf("could not resolve backendNodeId for selector %q", selector)
	}

	// Query the AX subtree rooted at this node.
	_, _ = e.callPageClient(ctx, pageClient, "Accessibility.enable", nil)
	axRaw, err := e.callPageClient(ctx, pageClient, "Accessibility.queryAXTree", map[string]any{
		"backendNodeId": descPayload.Node.BackendNodeID,
	})
	if err != nil {
		return "", nil, fmt.Errorf("Accessibility.queryAXTree: %w", err)
	}
	var axPayload struct {
		Nodes []axNode `json:"nodes"`
	}
	if err := json.Unmarshal(axRaw, &axPayload); err != nil {
		return "", nil, fmt.Errorf("decode Accessibility.queryAXTree: %w", err)
	}
	if len(axPayload.Nodes) == 0 {
		return "", nil, fmt.Errorf("empty AX subtree for selector %q", selector)
	}

	title, _ := e.evaluateString(ctx, pageClient, `(function(){ return document.title || ""; })()`)
	snapshot, meta := buildAXSnapshot(axPayload.Nodes, interactiveOnly, compact, maxDepth, title)
	if strings.TrimSpace(snapshot) == "" {
		return "", nil, fmt.Errorf("empty AX snapshot for selector %q", selector)
	}
	return snapshot, meta, nil
}

func (e *Executor) syncAriaRefMeta(ctx context.Context, pageClient *cdp.Client, meta map[string]map[string]any) error {
	if pageClient == nil {
		return fmt.Errorf("nil page client")
	}

	payload, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal aria ref metadata: %w", err)
	}

	expr := `(function(meta){
  var root = { __url: location.href, __createdAt: Date.now() };
  if (meta && typeof meta === "object") {
    for (var key in meta) {
      if (!Object.prototype.hasOwnProperty.call(meta, key)) continue;
      root[key] = meta[key];
    }
  }
  window.__ariaRefs = {};
  window.__ariaRefMeta = root;
  return true;
})(` + string(payload) + `)`

	okSet, err := e.evaluateBool(ctx, pageClient, expr)
	if err != nil {
		return err
	}
	if !okSet {
		return fmt.Errorf("set aria ref metadata failed")
	}
	return nil
}

func buildAXSnapshot(nodes []axNode, interactiveOnly bool, compact bool, maxDepth int, title string) (string, map[string]map[string]any) {
	if maxDepth < 1 {
		maxDepth = 1
	}

	indexByNodeID := make(map[string]axNode, len(nodes))
	parentByNodeID := make(map[string]string, len(nodes))
	orderByNodeID := make(map[string]int, len(nodes))
	for idx, n := range nodes {
		id := strings.TrimSpace(n.NodeID)
		if id == "" {
			continue
		}
		indexByNodeID[id] = n
		orderByNodeID[id] = idx
		for _, child := range n.ChildIDs {
			child = strings.TrimSpace(child)
			if child == "" {
				continue
			}
			if _, exists := parentByNodeID[child]; !exists {
				parentByNodeID[child] = id
			}
		}
	}

	roots := make([]string, 0, len(indexByNodeID))
	for id := range indexByNodeID {
		if _, ok := parentByNodeID[id]; !ok {
			roots = append(roots, id)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return orderByNodeID[roots[i]] < orderByNodeID[roots[j]]
	})

	lines := []string{
		`- document "` + clampAXText(title, 120) + `"`,
	}
	meta := make(map[string]map[string]any, maxAriaSnapshotRefs)
	if len(roots) == 0 {
		return strings.Join(lines, "\n"), meta
	}

	seen := make(map[string]bool, len(indexByNodeID))
	keyOrdinal := map[string]int{}
	refCounter := 0

	var walk func(nodeID string, depth int)
	walk = func(nodeID string, depth int) {
		if nodeID == "" || depth > maxDepth {
			return
		}
		if len(lines) >= maxAriaSnapshotLines {
			return
		}
		if seen[nodeID] {
			return
		}
		seen[nodeID] = true

		node, ok := indexByNodeID[nodeID]
		if !ok {
			return
		}

		role := normalizeAXRole(axValueString(node.Role))
		name := clampAXText(axValueString(node.Name), 80)
		if shouldIncludeAXNode(node, role, name, interactiveOnly) && refCounter < maxAriaSnapshotRefs {
			refCounter++
			ref := fmt.Sprintf("e%d", refCounter)
			key := role + "|" + normalizeAXText(name)
			nth := keyOrdinal[key]
			keyOrdinal[key] = nth + 1

			line := strings.Repeat("  ", depth) + `- ` + role + ` "` + name + `"` + ` [ref=` + ref + `]`
			if !compact {
				extras := make([]string, 0, 2)
				if node.BackendDOMNodeID > 0 {
					extras = append(extras, fmt.Sprintf("backendNodeId=%d", node.BackendDOMNodeID))
				}
				v := clampAXText(axValueString(node.Value), 60)
				if v != "" {
					extras = append(extras, "value="+v)
				}
				if len(extras) > 0 {
					line += " (" + strings.Join(extras, ", ") + ")"
				}
			}
			lines = append(lines, line)

			entry := map[string]any{
				"role": role,
				"name": name,
				"nth":  nth,
			}
			if strings.TrimSpace(node.FrameID) != "" {
				entry["frameId"] = strings.TrimSpace(node.FrameID)
			}
			if node.BackendDOMNodeID > 0 {
				entry["backendNodeId"] = node.BackendDOMNodeID
			}
			meta[ref] = entry
		}

		for _, childID := range node.ChildIDs {
			walk(strings.TrimSpace(childID), depth+1)
		}
	}

	for _, rootID := range roots {
		walk(rootID, 1)
		if len(lines) >= maxAriaSnapshotLines {
			break
		}
	}

	if len(lines) > maxAriaSnapshotLines {
		lines = lines[:maxAriaSnapshotLines]
	}
	return strings.Join(lines, "\n"), meta
}

func shouldIncludeAXNode(node axNode, role string, name string, interactiveOnly bool) bool {
	if node.Ignored {
		return false
	}
	if role == "" || role == "none" || role == "generic" || role == "inlinetextbox" {
		return false
	}
	if role == "statictext" && strings.TrimSpace(name) == "" {
		return false
	}
	if interactiveOnly {
		return isInteractiveAXRole(role)
	}
	return isInteractiveAXRole(role) || isSemanticAXRole(role)
}

func isInteractiveAXRole(role string) bool {
	switch role {
	case "button", "link", "textbox", "searchbox", "combobox", "listbox", "option", "checkbox", "radio", "switch", "slider", "spinbutton", "menuitem", "menuitemcheckbox", "menuitemradio", "tab", "treeitem":
		return true
	default:
		return false
	}
}

func isSemanticAXRole(role string) bool {
	switch role {
	case "rootwebarea", "document", "main", "navigation", "banner", "contentinfo", "form", "dialog", "heading", "article", "region", "list", "listitem", "table", "row", "cell":
		return true
	default:
		return false
	}
}

func axValueString(v *axValue) string {
	if v == nil {
		return ""
	}
	raw := strings.TrimSpace(string(v.Value))
	if raw == "" || raw == "null" {
		return ""
	}

	var s string
	if err := json.Unmarshal(v.Value, &s); err == nil {
		return s
	}

	var b bool
	if err := json.Unmarshal(v.Value, &b); err == nil {
		if b {
			return "true"
		}
		return "false"
	}

	var f float64
	if err := json.Unmarshal(v.Value, &f); err == nil {
		if f == float64(int64(f)) {
			return fmt.Sprintf("%d", int64(f))
		}
		return fmt.Sprintf("%g", f)
	}

	return strings.Trim(raw, `"`)
}

func normalizeAXRole(role string) string {
	return normalizeAXText(role)
}

func normalizeAXText(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	return strings.ToLower(strings.TrimSpace(strings.Join(strings.Fields(text), " ")))
}

func clampAXText(text string, maxLen int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(strings.ReplaceAll(text, "\n", " "), "\r", " ")), " "))
	if maxLen <= 0 || len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}
