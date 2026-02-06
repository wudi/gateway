package protocol

import (
	"fmt"
	"sync"
)

// Factory creates a new Translator instance.
type Factory func() Translator

var (
	factories   = make(map[string]Factory)
	factoriesMu sync.RWMutex
)

// Register registers a protocol translator factory.
// This should be called from init() in the protocol implementation package.
func Register(protocolType string, factory Factory) {
	factoriesMu.Lock()
	defer factoriesMu.Unlock()
	factories[protocolType] = factory
}

// New creates a new Translator for the given protocol type.
func New(protocolType string) (Translator, error) {
	factoriesMu.RLock()
	factory, ok := factories[protocolType]
	factoriesMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown protocol type: %s", protocolType)
	}
	return factory(), nil
}

// RegisteredTypes returns a list of registered protocol types.
func RegisteredTypes() []string {
	factoriesMu.RLock()
	defer factoriesMu.RUnlock()

	types := make([]string, 0, len(factories))
	for t := range factories {
		types = append(types, t)
	}
	return types
}
