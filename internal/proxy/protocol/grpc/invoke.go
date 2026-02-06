package grpc

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// invoker handles dynamic gRPC invocation.
type invoker struct {
	marshalOpts   protojson.MarshalOptions
	unmarshalOpts protojson.UnmarshalOptions
}

func newInvoker() *invoker {
	return &invoker{
		marshalOpts: protojson.MarshalOptions{
			EmitUnpopulated: false,
			UseProtoNames:   true,
		},
		unmarshalOpts: protojson.UnmarshalOptions{
			DiscardUnknown: true,
		},
	}
}

// invokeUnary performs a unary RPC call.
func (inv *invoker) invokeUnary(
	ctx context.Context,
	conn *grpc.ClientConn,
	md protoreflect.MethodDescriptor,
	jsonBody []byte,
) ([]byte, error) {
	// Build input message from JSON
	inputDesc := md.Input()
	inputMsg := dynamicpb.NewMessage(inputDesc)

	if len(jsonBody) > 0 {
		if err := inv.unmarshalOpts.Unmarshal(jsonBody, inputMsg); err != nil {
			return nil, fmt.Errorf("failed to parse JSON request: %w", err)
		}
	}

	// Build output message
	outputDesc := md.Output()
	outputMsg := dynamicpb.NewMessage(outputDesc)

	// Build the full method name: /package.Service/Method
	fullMethod := fmt.Sprintf("/%s/%s", md.Parent().FullName(), md.Name())

	// Invoke the RPC
	if err := conn.Invoke(ctx, fullMethod, inputMsg, outputMsg); err != nil {
		return nil, err
	}

	// Marshal response to JSON
	jsonResp, err := inv.marshalOpts.Marshal(outputMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response to JSON: %w", err)
	}

	return jsonResp, nil
}

// dynamicCodec is a gRPC codec that handles dynamic proto messages.
type dynamicCodec struct{}

func (dynamicCodec) Marshal(v interface{}) ([]byte, error) {
	msg, ok := v.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("dynamicCodec: expected proto.Message, got %T", v)
	}
	return proto.Marshal(msg)
}

func (dynamicCodec) Unmarshal(data []byte, v interface{}) error {
	msg, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("dynamicCodec: expected proto.Message, got %T", v)
	}
	return proto.Unmarshal(data, msg)
}

func (dynamicCodec) Name() string {
	return "proto"
}
