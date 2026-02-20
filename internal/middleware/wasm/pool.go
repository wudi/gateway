package wasm

import (
	"context"
	"sync/atomic"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// InstancePool manages a channel-based pool of pre-instantiated WASM module instances.
// Channel-based (not sync.Pool) because WASM instances are expensive and must not be GC'd.
type InstancePool struct {
	runtime   wazero.Runtime
	compiled  wazero.CompiledModule
	instances chan api.Module

	borrows    atomic.Int64
	returns    atomic.Int64
	poolMisses atomic.Int64
}

// NewInstancePool pre-instantiates `size` modules into a buffered channel.
func NewInstancePool(ctx context.Context, rt wazero.Runtime, compiled wazero.CompiledModule, size int) (*InstancePool, error) {
	if size <= 0 {
		size = 4
	}
	pool := &InstancePool{
		runtime:   rt,
		compiled:  compiled,
		instances: make(chan api.Module, size),
	}

	for i := 0; i < size; i++ {
		mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
		if err != nil {
			pool.Close(ctx)
			return nil, err
		}
		pool.instances <- mod
	}

	return pool, nil
}

// Borrow returns an instance from the pool. If the pool is empty,
// a new instance is created on-the-fly (no rejection).
func (p *InstancePool) Borrow(ctx context.Context) (api.Module, error) {
	p.borrows.Add(1)
	select {
	case mod := <-p.instances:
		return mod, nil
	default:
		p.poolMisses.Add(1)
		return p.runtime.InstantiateModule(ctx, p.compiled, wazero.NewModuleConfig().WithName(""))
	}
}

// Return puts an instance back into the pool. If the pool is full, the excess
// instance is closed.
func (p *InstancePool) Return(ctx context.Context, mod api.Module) {
	p.returns.Add(1)
	select {
	case p.instances <- mod:
	default:
		mod.Close(ctx)
	}
}

// Close drains and closes all instances in the pool.
func (p *InstancePool) Close(ctx context.Context) {
	close(p.instances)
	for mod := range p.instances {
		mod.Close(ctx)
	}
}

// PoolStats returns pool usage statistics.
type PoolStats struct {
	Borrows    int64 `json:"borrows"`
	Returns    int64 `json:"returns"`
	PoolMisses int64 `json:"pool_misses"`
	PoolSize   int   `json:"pool_size"`
}

// Stats returns current pool statistics.
func (p *InstancePool) Stats() PoolStats {
	return PoolStats{
		Borrows:    p.borrows.Load(),
		Returns:    p.returns.Load(),
		PoolMisses: p.poolMisses.Load(),
		PoolSize:   len(p.instances),
	}
}
