package grpcjson

import (
	"github.com/wudi/runway/internal/proxy/protocol"
)

func init() {
	protocol.Register("grpc_json", func() protocol.Translator {
		return New()
	})
}
