package rest

import (
	"github.com/wudi/runway/internal/proxy/protocol"
)

func init() {
	protocol.Register("grpc_to_rest", func() protocol.Translator {
		return New()
	})
}
