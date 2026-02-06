package grpc

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/sync/errgroup"
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

// invokeServerStream handles server-streaming RPC: 1 request, N responses.
func (inv *invoker) invokeServerStream(
	ctx context.Context,
	conn *grpc.ClientConn,
	md protoreflect.MethodDescriptor,
	jsonBody []byte,
	writer *ndjsonWriter,
) error {
	// Build input message from JSON
	inputDesc := md.Input()
	inputMsg := dynamicpb.NewMessage(inputDesc)

	if len(jsonBody) > 0 {
		if err := inv.unmarshalOpts.Unmarshal(jsonBody, inputMsg); err != nil {
			return fmt.Errorf("failed to parse JSON request: %w", err)
		}
	}

	// Build the full method name: /package.Service/Method
	fullMethod := fmt.Sprintf("/%s/%s", md.Parent().FullName(), md.Name())

	// Create stream descriptor for server streaming
	streamDesc := &grpc.StreamDesc{
		StreamName:    string(md.Name()),
		ServerStreams: true,
		ClientStreams: false,
	}

	// Create the stream
	stream, err := conn.NewStream(ctx, streamDesc, fullMethod)
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}

	// Send the single request
	if err := stream.SendMsg(inputMsg); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	// Close send direction
	if err := stream.CloseSend(); err != nil {
		return fmt.Errorf("failed to close send: %w", err)
	}

	// Receive all responses
	outputDesc := md.Output()
	for {
		outputMsg := dynamicpb.NewMessage(outputDesc)
		if err := stream.RecvMsg(outputMsg); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		if err := writer.WriteMessage(outputMsg); err != nil {
			return fmt.Errorf("failed to write response: %w", err)
		}
	}

	return nil
}

// invokeClientStream handles client-streaming RPC: N requests, 1 response.
func (inv *invoker) invokeClientStream(
	ctx context.Context,
	conn *grpc.ClientConn,
	md protoreflect.MethodDescriptor,
	reader *ndjsonReader,
) ([]byte, error) {
	// Build the full method name
	fullMethod := fmt.Sprintf("/%s/%s", md.Parent().FullName(), md.Name())

	// Create stream descriptor for client streaming
	streamDesc := &grpc.StreamDesc{
		StreamName:    string(md.Name()),
		ServerStreams: false,
		ClientStreams: true,
	}

	// Create the stream
	stream, err := conn.NewStream(ctx, streamDesc, fullMethod)
	if err != nil {
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}

	// Send all messages from the reader
	inputDesc := md.Input()
	for {
		inputMsg := dynamicpb.NewMessage(inputDesc)
		if err := reader.ReadMessage(inputMsg); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read request: %w", err)
		}

		if err := stream.SendMsg(inputMsg); err != nil {
			return nil, fmt.Errorf("failed to send message: %w", err)
		}
	}

	// Close send direction
	if err := stream.CloseSend(); err != nil {
		return nil, fmt.Errorf("failed to close send: %w", err)
	}

	// Receive the single response
	outputDesc := md.Output()
	outputMsg := dynamicpb.NewMessage(outputDesc)
	if err := stream.RecvMsg(outputMsg); err != nil {
		return nil, err
	}

	// Marshal response to JSON
	jsonResp, err := inv.marshalOpts.Marshal(outputMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response to JSON: %w", err)
	}

	return jsonResp, nil
}

// invokeBidiStream handles bidirectional streaming RPC: N requests, N responses.
func (inv *invoker) invokeBidiStream(
	ctx context.Context,
	conn *grpc.ClientConn,
	md protoreflect.MethodDescriptor,
	reader *ndjsonReader,
	writer *ndjsonWriter,
) error {
	// Build the full method name
	fullMethod := fmt.Sprintf("/%s/%s", md.Parent().FullName(), md.Name())

	// Create stream descriptor for bidirectional streaming
	streamDesc := &grpc.StreamDesc{
		StreamName:    string(md.Name()),
		ServerStreams: true,
		ClientStreams: true,
	}

	// Create the stream
	stream, err := conn.NewStream(ctx, streamDesc, fullMethod)
	if err != nil {
		return fmt.Errorf("failed to create stream: %w", err)
	}

	// Use errgroup for concurrent send/receive
	g, ctx := errgroup.WithContext(ctx)

	// Send goroutine
	g.Go(func() error {
		defer stream.CloseSend()

		inputDesc := md.Input()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			inputMsg := dynamicpb.NewMessage(inputDesc)
			if err := reader.ReadMessage(inputMsg); err != nil {
				if err == io.EOF {
					return nil
				}
				return fmt.Errorf("failed to read request: %w", err)
			}

			if err := stream.SendMsg(inputMsg); err != nil {
				return fmt.Errorf("failed to send message: %w", err)
			}
		}
	})

	// Receive goroutine
	g.Go(func() error {
		outputDesc := md.Output()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			outputMsg := dynamicpb.NewMessage(outputDesc)
			if err := stream.RecvMsg(outputMsg); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}

			if err := writer.WriteMessage(outputMsg); err != nil {
				return fmt.Errorf("failed to write response: %w", err)
			}
		}
	})

	return g.Wait()
}
