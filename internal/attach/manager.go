package attach

import (
	"sync"

	"github.com/amux-run/amux/internal/observability"
)

// Manager owns the set of attach surfaces for the daemon, one per terminal
// surface, keyed by surface id. It is a thin registry: every surface is
// independent (its own ring, lease, and observers), and all sharing one
// Registry share the attach gauges/counters. Safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	reg      *observability.Registry
	surfaces map[string]*Surface
}

// NewManager returns an empty manager. reg may be nil to disable metrics.
func NewManager(reg *observability.Registry) *Manager {
	return &Manager{reg: reg, surfaces: make(map[string]*Surface)}
}

// AddSurface registers a new surface. It returns ErrSurfaceExists if the id is
// already registered, or the SurfaceConfig validation error.
func (m *Manager) AddSurface(cfg SurfaceConfig) (*Surface, error) {
	s, err := NewSurface(cfg, m.reg)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.surfaces[cfg.ID]; ok {
		return nil, ErrSurfaceExists
	}
	m.surfaces[cfg.ID] = s
	return s, nil
}

// Surface returns the registered surface for id.
func (m *Manager) Surface(id string) (*Surface, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.surfaces[id]
	return s, ok
}

// RemoveSurface unregisters and closes the surface for id (disconnecting its
// attachments). It is a manager-level teardown, distinct from a client detach.
func (m *Manager) RemoveSurface(id string) {
	m.mu.Lock()
	s, ok := m.surfaces[id]
	if ok {
		delete(m.surfaces, id)
	}
	m.mu.Unlock()
	if ok {
		s.Close()
	}
}

// Surfaces returns the number of registered surfaces.
func (m *Manager) Surfaces() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.surfaces)
}
