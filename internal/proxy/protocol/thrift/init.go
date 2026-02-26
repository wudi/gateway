package thrift

import (
	"github.com/wudi/runway/internal/proxy/protocol"
)

func init() {
	protocol.Register("http_to_thrift", func() protocol.Translator {
		return New()
	})
}
