package thrift

import (
	"github.com/wudi/gateway/internal/proxy/protocol"
)

func init() {
	protocol.Register("http_to_thrift", func() protocol.Translator {
		return New()
	})
}
