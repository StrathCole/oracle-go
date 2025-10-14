package sources

import (
	"fmt"
	"sync"
)

var (
	registry = make(map[string]SourceFactory)
	mu       sync.RWMutex
)

// Register adds a source factory to the registry
func Register(name string, factory SourceFactory) {
	mu.Lock()
	defer mu.Unlock()
	registry[name] = factory
}

// Create creates a new source instance by name
func Create(sourceType, name string, config map[string]interface{}) (Source, error) {
	mu.RLock()
	defer mu.RUnlock()

	key := fmt.Sprintf("%s.%s", sourceType, name)
	factory, ok := registry[key]
	if !ok {
		return nil, fmt.Errorf("unknown source: %s", key)
	}

	return factory(config)
}

// List returns all registered source names
func List() []string {
	mu.RLock()
	defer mu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}
