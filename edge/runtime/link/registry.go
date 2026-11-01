package link

import (
	"fmt"
	"sort"
	"sync"
)

// Factory builds a driver from its link configuration.
type Factory func(cfg LinkConfig) (Driver, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register makes a driver available under its driver id. Drivers register themselves in init;
// registering an id twice panics.
func Register(driver string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if driver == "" || factory == nil {
		panic("link: Register needs a driver id and a factory")
	}
	if _, taken := registry[driver]; taken {
		panic("link: driver registered twice: " + driver)
	}
	registry[driver] = factory
}

// Registered reports whether a driver id is known.
func Registered(driver string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := registry[driver]
	return ok
}

// New builds the driver cfg.Driver names; an unknown driver id is an error.
func New(cfg LinkConfig) (Driver, error) {
	registryMu.RLock()
	factory, ok := registry[cfg.Driver]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("link %q: no driver %q (known: %v)", cfg.ID, cfg.Driver, Drivers())
	}
	d, err := factory(cfg)
	if err != nil {
		return nil, fmt.Errorf("link %q (%s): %w", cfg.ID, cfg.Driver, err)
	}
	return d, nil
}

// Drivers lists the registered driver ids.
func Drivers() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
