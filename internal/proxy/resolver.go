package proxy

import (
	"context"
	"net"
	"sync/atomic"
	"time"
)

// NewResolver builds a custom *net.Resolver that round-robins across the given
// nameservers. If nameservers is empty, it returns nil (use OS default).
func NewResolver(nameservers []string, timeout time.Duration) *net.Resolver {
	if len(nameservers) == 0 {
		return nil
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	var counter atomic.Uint64

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			idx := counter.Add(1) - 1
			ns := nameservers[idx%uint64(len(nameservers))]

			d := net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, "udp", ns)
		},
	}
}
