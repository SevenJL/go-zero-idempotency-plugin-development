package codec

import (
	"sync"

	"github.com/your-org/go-idempotency/application/port"
)

type registryEntry struct {
	codec   port.RPCCodec
	factory func() any
}

// DefaultCodecRegistry is a thread-safe implementation of port.RPCCodecRegistry.
type DefaultCodecRegistry struct {
	mu       sync.RWMutex
	entries  map[string]registryEntry
	fallback port.RPCCodec
}

// NewCodecRegistry creates a registry that falls back to the provided codec
// when a method is not explicitly registered. If fallback is nil, JSONCodec
// is used.
func NewCodecRegistry(fallback port.RPCCodec) *DefaultCodecRegistry {
	if fallback == nil {
		fallback = JSONCodec{}
	}
	return &DefaultCodecRegistry{
		entries:  make(map[string]registryEntry),
		fallback: fallback,
	}
}

func (r *DefaultCodecRegistry) Lookup(fullMethod string) (port.RPCCodec, func() any, bool) {
	r.mu.RLock()
	entry, ok := r.entries[fullMethod]
	r.mu.RUnlock()
	if ok {
		return entry.codec, entry.factory, true
	}
	// Fallback: return the default codec with no factory (caller can handle nil).
	return r.fallback, nil, false
}

func (r *DefaultCodecRegistry) Register(fullMethod string, codec port.RPCCodec, factory func() any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[fullMethod] = registryEntry{codec: codec, factory: factory}
}

var _ port.RPCCodecRegistry = (*DefaultCodecRegistry)(nil)
