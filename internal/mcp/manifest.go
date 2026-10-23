package mcp

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// ManifestEntry is one registered tool: its area and its authorization posture, read from
// domain.ToolPosture (the one per-tool classification; this index holds no second copy of it).
type ManifestEntry struct {
	Name string
	Area string
	domain.Posture
}

// manifest is the tools Tool registered, by name.
type manifest struct {
	mu      sync.Mutex
	entries map[string]ManifestEntry
}

func (m *manifest) add(e ManifestEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, dup := m.entries[e.Name]; dup {
		panic(fmt.Sprintf("mcp: tool %s registered twice (areas %s and %s)", e.Name, prev.Area, e.Area))
	}
	m.entries[e.Name] = e
}

// Manifest is every tool registered through Tool, sorted by name.
func (s *Server) Manifest() []ManifestEntry {
	s.manifest.mu.Lock()
	defer s.manifest.mu.Unlock()
	out := make([]ManifestEntry, 0, len(s.manifest.entries))
	for _, e := range s.manifest.entries {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b ManifestEntry) int { return strings.Compare(a.Name, b.Name) })
	return out
}
