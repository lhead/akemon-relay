package relay

import (
	"github.com/akemon/akemon-relay/internal/config"
	"github.com/akemon/akemon-relay/internal/store"
)

// Relay is the core coordination layer.
type Relay struct {
	Registry *Registry
	Store    *store.Store
	Config   *config.Config
}

func New(cfg *config.Config, st *store.Store) *Relay {
	return &Relay{
		Registry: NewRegistry(),
		Store:    st,
		Config:   cfg,
	}
}
