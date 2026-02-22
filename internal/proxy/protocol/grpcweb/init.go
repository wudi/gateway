package grpcweb

import (
	"github.com/wudi/gateway/internal/proxy/protocol"
)

func init() {
	protocol.Register("grpc_web", func() protocol.Translator {
		return New()
	})
}
