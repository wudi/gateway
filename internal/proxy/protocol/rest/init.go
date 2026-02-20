package rest

import (
	"github.com/wudi/gateway/internal/proxy/protocol"
)

func init() {
	protocol.Register("grpc_to_rest", func() protocol.Translator {
		return New()
	})
}
