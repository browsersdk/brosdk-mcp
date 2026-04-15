package mcp

import "brosdk-mcp/internal/schema"

// Router is a placeholder for MCP request dispatch.
// P1 only wires schema-backed tool metadata into the runtime.
type Router struct {
	tools      map[string]schema.Tool
	toolsOrder []schema.Tool
}

func NewRouter(reg *schema.Registry) *Router {
	r := &Router{
		tools:      make(map[string]schema.Tool, len(reg.Tools)),
		toolsOrder: make([]schema.Tool, 0, len(reg.Tools)),
	}
	for _, tool := range reg.Tools {
		r.tools[tool.Name] = tool
		r.toolsOrder = append(r.toolsOrder, tool)
	}
	return r
}

func (r *Router) ToolCount() int {
	return len(r.tools)
}

func (r *Router) HasTool(name string) bool {
	_, ok := r.tools[name]
	return ok
}

func (r *Router) ToolList() []schema.Tool {
	out := make([]schema.Tool, len(r.toolsOrder))
	copy(out, r.toolsOrder)
	return out
}
