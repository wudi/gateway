package grpc

import (
	"github.com/wudi/runway/internal/proxy/protocol"
)

func init() {
	protocol.Register("http_to_grpc", func() protocol.Translator {
		return New()
	})
}
