package thrift

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/apache/thrift/lib/go/thrift"
)

// invoker handles Thrift RPC invocation over TProtocol.
type invoker struct {
	seqID atomic.Int32
}

func newInvoker() *invoker {
	return &invoker{}
}

// invokeMethod sends a Thrift RPC and reads the response.
func (inv *invoker) invokeMethod(ctx context.Context, iprot, oprot thrift.TProtocol, c *codec, schema *serviceSchema, methodName string, jsonBody []byte, multiplexed bool) ([]byte, error) {
	ms, ok := schema.methods[methodName]
	if !ok {
		return nil, thrift.NewTApplicationException(thrift.UNKNOWN_METHOD, fmt.Sprintf("unknown method: %s", methodName))
	}

	// Parse JSON body.
	var jsonData map[string]interface{}
	if len(jsonBody) > 0 {
		dec := json.NewDecoder(bytes.NewReader(jsonBody))
		dec.UseNumber()
		if err := dec.Decode(&jsonData); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		// Convert json.Number to float64 for consistent handling.
		jsonData = convertNumbers(jsonData)
	}
	if jsonData == nil {
		jsonData = make(map[string]interface{})
	}

	// Determine wire method name.
	wireName := methodName
	if multiplexed {
		wireName = schema.service.Name + ":" + methodName
	}

	// Determine message type.
	msgType := thrift.CALL
	if ms.oneway {
		msgType = thrift.ONEWAY
	}

	seqID := inv.seqID.Add(1)

	// Write the call.
	if err := oprot.WriteMessageBegin(ctx, wireName, msgType, seqID); err != nil {
		return nil, fmt.Errorf("WriteMessageBegin: %w", err)
	}
	if err := c.jsonToThriftArgs(ctx, oprot, ms.args, jsonData); err != nil {
		return nil, fmt.Errorf("encoding args: %w", err)
	}
	if err := oprot.WriteMessageEnd(ctx); err != nil {
		return nil, fmt.Errorf("WriteMessageEnd: %w", err)
	}
	if err := oprot.Flush(ctx); err != nil {
		return nil, fmt.Errorf("Flush: %w", err)
	}

	// For oneway methods, return immediately.
	if ms.oneway {
		return []byte("{}"), nil
	}

	// Read the reply.
	_, replyType, _, err := iprot.ReadMessageBegin(ctx)
	if err != nil {
		return nil, fmt.Errorf("ReadMessageBegin: %w", err)
	}

	if replyType == thrift.EXCEPTION {
		// Read TApplicationException.
		appEx := thrift.NewTApplicationException(0, "")
		if err := appEx.Read(ctx, iprot); err != nil {
			return nil, fmt.Errorf("reading application exception: %w", err)
		}
		if err := iprot.ReadMessageEnd(ctx); err != nil {
			return nil, fmt.Errorf("ReadMessageEnd after exception: %w", err)
		}
		return nil, appEx
	}

	if replyType != thrift.REPLY {
		return nil, fmt.Errorf("unexpected message type: %d", replyType)
	}

	result, err := c.readResult(ctx, iprot, ms)
	if err != nil {
		return nil, err
	}

	if err := iprot.ReadMessageEnd(ctx); err != nil {
		return nil, fmt.Errorf("ReadMessageEnd: %w", err)
	}

	return result, nil
}

// convertNumbers recursively converts json.Number to float64 in a map.
func convertNumbers(data map[string]interface{}) map[string]interface{} {
	for k, v := range data {
		data[k] = convertNumberValue(v)
	}
	return data
}

func convertNumberValue(v interface{}) interface{} {
	switch val := v.(type) {
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			return val.String()
		}
		return f
	case map[string]interface{}:
		return convertNumbers(val)
	case []interface{}:
		for i, item := range val {
			val[i] = convertNumberValue(item)
		}
		return val
	default:
		return v
	}
}
