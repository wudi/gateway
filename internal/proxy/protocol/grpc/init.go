package grpc

import (
	"github.com/example/gateway/internal/proxy/protocol"
)

func init() {
	protocol.Register("http_to_grpc", func() protocol.Translator {
		return New()
	})
}
