package relay

import (
	"sync"
	"time"
)

// graceEntry holds a disconnected agent's info during the grace period.
type graceEntry struct {
	accountID string
	expiresAt time.Time
}

// Registry manages online agents (in-memory hot data).
type Registry struct {
	mu     sync.RWMutex
	agents map[string]*ConnectedAgent // keyed by agent name

	graceMu sync.Mutex
	grace   map[string]*graceEntry // keyed by agent name
}

func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]*ConnectedAgent),
		grace:  make(map[string]*graceEntry),
	}
}

// Get returns a connected agent by name, or nil if not online.
func (r *Registry) Get(name string) *ConnectedAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[name]
}

// Register adds an agent to the registry.
// Returns the old agent (if any) that was displaced by same-account reconnect.
// Returns an error string if the name is held by a different account.
func (r *Registry) Register(agent *ConnectedAgent, gracePeriod time.Duration) (*ConnectedAgent, string) {
	name := agent.Name

	// Check grace period reservation
	r.graceMu.Lock()
	if g, ok := r.grace[name]; ok {
		if time.Now().Before(g.expiresAt) && g.accountID != agent.AccountID {
			r.graceMu.Unlock()
			return nil, "name already registered by another account (grace period)"
		}
		delete(r.grace, name)
	}
	r.graceMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.agents[name]; ok {
		if existing.AccountID != agent.AccountID {
			return nil, "name already registered by another account"
		}
		// Same account reconnect: displace old connection
		delete(r.agents, name)
		r.agents[name] = agent
		return existing, ""
	}

	r.agents[name] = agent
	return nil, ""
}

// Unregister removes an agent and starts the grace period.
func (r *Registry) Unregister(name string, gracePeriod time.Duration) {
	r.mu.Lock()
	agent, ok := r.agents[name]
	if ok {
		delete(r.agents, name)
	}
	r.mu.Unlock()

	if ok {
		r.graceMu.Lock()
		r.grace[name] = &graceEntry{
			accountID: agent.AccountID,
			expiresAt: time.Now().Add(gracePeriod),
		}
		r.graceMu.Unlock()
	}
}

// Online returns a list of all connected agent names.
func (r *Registry) Online() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	return names
}
