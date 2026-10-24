// Package tools is the MCP tool surface, one file per area: each area's file registers its hook
// (register in an init) and its Register<Area> adds that area's tools to the server through mcp.Tool,
// each a mapping of its request DTO to one service call and the response back. No role, grant,
// event or audit logic lives here; that is internal/service's.
package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// areas is every tool area a plan task names a file in this package for, in the order Register adds
// them, with the task that writes it. An area is pre-registered here so its lane adds a file, not a
// line here.
var areas = []struct{ name, owner string }{
	{"projects", "R1.1"},
	{"boards", "R1.1"},
	{"tasks", "R1.2"},
	{"lifecycle", "R1.4"},
	{"context", "R1.6"},
	{"media", "R1.8"},
	{"search", "R1.9"},
	{"artifacts", "R2.1"},
	{"comments", "R2.1"},
	{"followups", "R2.1"},
	{"runs", "R2.2"},
	{"sprints", "R2.2"},
	{"notifications", "R2.3"},
	{"agents_profile", "R2.3"},
	{"anomalies", "R2.3"},
	{"rank", "R2.3"},
	{"grants", "R2.4"},
	{"agents", "R2.5"},
	{"me", "R2.5"},
	{"audit", "R2.5"},
	{"chronicle", "R2.6"},
	{"integrations", "R2.8"},
	{"deliveries", "R2.8"},
}

// Hook adds one area's tools to s, each calling svc.
type Hook func(s *mcp.Server, svc service.Services)

// hooks is each area's hook, by name, set by the area files' init.
var hooks = map[string]Hook{}

// register hands an area's hook to Register. A name not in areas, or registered twice, panics at
// init: a hook Register would never call is a surface that is never served.
func register(name string, h Hook) {
	if _, dup := hooks[name]; dup {
		panic("tools: area " + name + " registered twice")
	}
	for _, a := range areas {
		if a.name == name {
			hooks[name] = h
			return
		}
	}
	panic("tools: area " + name + " is not in areas")
}

// Register adds every area's tools to s, in areas' order; an area whose file is not written yet
// adds none.
func Register(s *mcp.Server, svc service.Services) {
	for _, a := range areas {
		if h, ok := hooks[a.name]; ok {
			h(s, svc)
		}
	}
}
