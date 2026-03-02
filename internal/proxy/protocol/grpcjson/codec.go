package grpcjson

import "fmt"

// jsonCodec is a gRPC codec that passes raw JSON bytes through without transformation.
// When used with grpc.ForceCodec, it sets the wire content-type to application/grpc+json.
// The backend gRPC server must also register a codec named "json" via encoding.RegisterCodec.
type jsonCodec struct{}

func (jsonCodec) Marshal(v interface{}) ([]byte, error) {
	b, ok := v.(*[]byte)
	if !ok {
		return nil, fmt.Errorf("jsonCodec: expected *[]byte, got %T", v)
	}
	return *b, nil
}

func (jsonCodec) Unmarshal(data []byte, v interface{}) error {
	b, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("jsonCodec: expected *[]byte, got %T", v)
	}
	*b = append((*b)[:0], data...)
	return nil
}

// Name returns "json" — this determines the gRPC wire content-type (application/grpc+json).
func (jsonCodec) Name() string { return "json" }
