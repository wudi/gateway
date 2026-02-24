//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/gateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	rpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	// Register the grpc translator
	_ "github.com/wudi/gateway/internal/proxy/protocol/grpc"
)

// testStreamServer is a simple gRPC server for streaming tests.
type testStreamServer struct {
	listener   net.Listener
	grpcServer *grpc.Server
	port       int
	files      *protoregistry.Files
	msgDesc    protoreflect.MessageDescriptor
}

// startTestStreamServer starts a gRPC server with streaming methods for testing.
func startTestStreamServer(t *testing.T) *testStreamServer {
	t.Helper()

	// Find an available port
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port

	// Build file descriptor for test service
	files, msgDesc := buildStreamingServiceDescriptor(t)

	// Create gRPC server
	s := grpc.NewServer()

	// Register our dynamic service handler first
	registerStreamingService(s, files, msgDesc)

	// Register custom reflection service that includes our dynamic service
	registerCustomReflection(s, files)

	// Start server
	go func() {
		if err := s.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			t.Logf("grpc server error: %v", err)
		}
	}()

	return &testStreamServer{
		listener:   lis,
		grpcServer: s,
		port:       port,
		files:      files,
		msgDesc:    msgDesc,
	}
}

// registerCustomReflection registers a reflection service that includes dynamically defined services.
func registerCustomReflection(s *grpc.Server, files *protoregistry.Files) {
	refSvc := &customReflectionServer{files: files}
	rpb.RegisterServerReflectionServer(s, refSvc)
}

// customReflectionServer implements the reflection service for dynamic descriptors.
type customReflectionServer struct {
	rpb.UnimplementedServerReflectionServer
	files *protoregistry.Files
}

func (s *customReflectionServer) ServerReflectionInfo(stream rpb.ServerReflection_ServerReflectionInfoServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		var resp *rpb.ServerReflectionResponse
		switch r := req.MessageRequest.(type) {
		case *rpb.ServerReflectionRequest_ListServices:
			resp = s.listServices(req)
		case *rpb.ServerReflectionRequest_FileContainingSymbol:
			resp = s.fileContainingSymbol(req, r.FileContainingSymbol)
		default:
			resp = &rpb.ServerReflectionResponse{
				ValidHost:       req.Host,
				OriginalRequest: req,
				MessageResponse: &rpb.ServerReflectionResponse_ErrorResponse{
					ErrorResponse: &rpb.ErrorResponse{
						ErrorCode:    int32(codes.Unimplemented),
						ErrorMessage: "not implemented",
					},
				},
			}
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

func (s *customReflectionServer) listServices(req *rpb.ServerReflectionRequest) *rpb.ServerReflectionResponse {
	var services []*rpb.ServiceResponse
	s.files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for i := 0; i < fd.Services().Len(); i++ {
			sd := fd.Services().Get(i)
			services = append(services, &rpb.ServiceResponse{
				Name: string(sd.FullName()),
			})
		}
		return true
	})

	return &rpb.ServerReflectionResponse{
		ValidHost:       req.Host,
		OriginalRequest: req,
		MessageResponse: &rpb.ServerReflectionResponse_ListServicesResponse{
			ListServicesResponse: &rpb.ListServiceResponse{
				Service: services,
			},
		},
	}
}

func (s *customReflectionServer) fileContainingSymbol(req *rpb.ServerReflectionRequest, symbol string) *rpb.ServerReflectionResponse {
	var foundFd protoreflect.FileDescriptor
	s.files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		// Check services
		for i := 0; i < fd.Services().Len(); i++ {
			if string(fd.Services().Get(i).FullName()) == symbol {
				foundFd = fd
				return false
			}
		}
		// Check messages
		for i := 0; i < fd.Messages().Len(); i++ {
			if string(fd.Messages().Get(i).FullName()) == symbol {
				foundFd = fd
				return false
			}
		}
		return true
	})

	if foundFd == nil {
		return &rpb.ServerReflectionResponse{
			ValidHost:       req.Host,
			OriginalRequest: req,
			MessageResponse: &rpb.ServerReflectionResponse_ErrorResponse{
				ErrorResponse: &rpb.ErrorResponse{
					ErrorCode:    int32(codes.NotFound),
					ErrorMessage: fmt.Sprintf("symbol %q not found", symbol),
				},
			},
		}
	}

	// Serialize the file descriptor
	fdp := protodesc.ToFileDescriptorProto(foundFd)
	fdBytes, _ := proto.Marshal(fdp)

	return &rpb.ServerReflectionResponse{
		ValidHost:       req.Host,
		OriginalRequest: req,
		MessageResponse: &rpb.ServerReflectionResponse_FileDescriptorResponse{
			FileDescriptorResponse: &rpb.FileDescriptorResponse{
				FileDescriptorProto: [][]byte{fdBytes},
			},
		},
	}
}

func (s *testStreamServer) Stop() {
	s.grpcServer.Stop()
	s.listener.Close()
}

func (s *testStreamServer) Address() string {
	return fmt.Sprintf("127.0.0.1:%d", s.port)
}

// buildStreamingServiceDescriptor creates a file descriptor for a streaming test service.
func buildStreamingServiceDescriptor(t *testing.T) (*protoregistry.Files, protoreflect.MessageDescriptor) {
	t.Helper()

	// Define the proto file programmatically
	fileDesc := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("streaming_test.proto"),
		Package: proto.String("streamtest"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("StreamMessage"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("id"),
						Number: proto.Int32(1),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					},
					{
						Name:   proto.String("data"),
						Number: proto.Int32(2),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					},
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("StreamService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:            proto.String("ServerStream"),
						InputType:       proto.String(".streamtest.StreamMessage"),
						OutputType:      proto.String(".streamtest.StreamMessage"),
						ServerStreaming: proto.Bool(true),
					},
					{
						Name:            proto.String("ClientStream"),
						InputType:       proto.String(".streamtest.StreamMessage"),
						OutputType:      proto.String(".streamtest.StreamMessage"),
						ClientStreaming: proto.Bool(true),
					},
					{
						Name:            proto.String("BidiStream"),
						InputType:       proto.String(".streamtest.StreamMessage"),
						OutputType:      proto.String(".streamtest.StreamMessage"),
						ServerStreaming: proto.Bool(true),
						ClientStreaming: proto.Bool(true),
					},
					{
						Name:       proto.String("Unary"),
						InputType:  proto.String(".streamtest.StreamMessage"),
						OutputType: proto.String(".streamtest.StreamMessage"),
					},
				},
			},
		},
	}

	files, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{fileDesc}})
	if err != nil {
		t.Fatalf("failed to build file descriptors: %v", err)
	}

	fd, err := files.FindFileByPath("streaming_test.proto")
	if err != nil {
		t.Fatalf("failed to find file: %v", err)
	}

	return files, fd.Messages().Get(0)
}

// registerStreamingService registers handlers for the streaming service.
func registerStreamingService(s *grpc.Server, files *protoregistry.Files, msgDesc protoreflect.MessageDescriptor) {
	fd, _ := files.FindFileByPath("streaming_test.proto")
	sd := fd.Services().Get(0)

	// Server streaming handler
	serverStreamDesc := &grpc.StreamDesc{
		StreamName:    "ServerStream",
		Handler:       serverStreamHandler(msgDesc),
		ServerStreams: true,
	}

	// Client streaming handler
	clientStreamDesc := &grpc.StreamDesc{
		StreamName:    "ClientStream",
		Handler:       clientStreamHandler(msgDesc),
		ClientStreams: true,
	}

	// Bidi streaming handler
	bidiStreamDesc := &grpc.StreamDesc{
		StreamName:    "BidiStream",
		Handler:       bidiStreamHandler(msgDesc),
		ServerStreams: true,
		ClientStreams: true,
	}

	// Register service with all handlers
	svcDesc := grpc.ServiceDesc{
		ServiceName: string(sd.FullName()),
		HandlerType: nil,
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Unary",
				Handler:    unaryHandler(msgDesc),
			},
		},
		Streams: []grpc.StreamDesc{
			*serverStreamDesc,
			*clientStreamDesc,
			*bidiStreamDesc,
		},
	}

	s.RegisterService(&svcDesc, nil)
}

func unaryHandler(msgDesc protoreflect.MessageDescriptor) func(interface{}, context.Context, func(interface{}) error, grpc.UnaryServerInterceptor) (interface{}, error) {
	return func(_ interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
		msg := dynamicpb.NewMessage(msgDesc)
		if err := dec(msg); err != nil {
			return nil, err
		}

		// Echo back with modified data
		resp := dynamicpb.NewMessage(msgDesc)
		idField := msgDesc.Fields().ByName("id")
		dataField := msgDesc.Fields().ByName("data")
		resp.Set(idField, msg.Get(idField))
		resp.Set(dataField, protoreflect.ValueOfString("unary:"+msg.Get(dataField).String()))
		return resp, nil
	}
}

func serverStreamHandler(msgDesc protoreflect.MessageDescriptor) grpc.StreamHandler {
	return func(_ interface{}, stream grpc.ServerStream) error {
		msg := dynamicpb.NewMessage(msgDesc)
		if err := stream.RecvMsg(msg); err != nil {
			return err
		}

		idField := msgDesc.Fields().ByName("id")
		dataField := msgDesc.Fields().ByName("data")
		baseID := int(msg.Get(idField).Int())
		baseData := msg.Get(dataField).String()

		// Send 3 responses
		for i := 0; i < 3; i++ {
			resp := dynamicpb.NewMessage(msgDesc)
			resp.Set(idField, protoreflect.ValueOfInt32(int32(baseID+i)))
			resp.Set(dataField, protoreflect.ValueOfString(fmt.Sprintf("server:%s:%d", baseData, i)))
			if err := stream.SendMsg(resp); err != nil {
				return err
			}
		}
		return nil
	}
}

func clientStreamHandler(msgDesc protoreflect.MessageDescriptor) grpc.StreamHandler {
	return func(_ interface{}, stream grpc.ServerStream) error {
		var count int
		var lastData string

		idField := msgDesc.Fields().ByName("id")
		dataField := msgDesc.Fields().ByName("data")

		for {
			msg := dynamicpb.NewMessage(msgDesc)
			if err := stream.RecvMsg(msg); err != nil {
				if err == io.EOF {
					break
				}
				return err
			}
			count++
			lastData = msg.Get(dataField).String()
		}

		// Send single response
		resp := dynamicpb.NewMessage(msgDesc)
		resp.Set(idField, protoreflect.ValueOfInt32(int32(count)))
		resp.Set(dataField, protoreflect.ValueOfString(fmt.Sprintf("client:received:%d:last:%s", count, lastData)))
		return stream.SendMsg(resp)
	}
}

func bidiStreamHandler(msgDesc protoreflect.MessageDescriptor) grpc.StreamHandler {
	return func(_ interface{}, stream grpc.ServerStream) error {
		idField := msgDesc.Fields().ByName("id")
		dataField := msgDesc.Fields().ByName("data")

		for {
			msg := dynamicpb.NewMessage(msgDesc)
			if err := stream.RecvMsg(msg); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}

			// Echo each message back immediately
			resp := dynamicpb.NewMessage(msgDesc)
			resp.Set(idField, msg.Get(idField))
			resp.Set(dataField, protoreflect.ValueOfString("bidi:"+msg.Get(dataField).String()))
			if err := stream.SendMsg(resp); err != nil {
				return err
			}
		}
	}
}

func TestServerStreamingIntegration(t *testing.T) {
	// Start test gRPC server
	server := startTestStreamServer(t)
	defer server.Stop()

	// Create gateway with route to streaming service
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-stream",
			Address:  ":0", // Let OS pick port
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 30 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:         "stream-test",
			Path:       "/api/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://" + server.Address(),
			}},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "streamtest.StreamService",
					Timeout: 10 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	// Test server streaming
	t.Run("server streaming returns NDJSON", func(t *testing.T) {
		body := `{"id":100,"data":"hello"}`
		req := httptest.NewRequest(http.MethodPost, "/api/ServerStream", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
			return
		}

		// Check content type
		ct := rec.Header().Get("Content-Type")
		if ct != "application/x-ndjson" {
			t.Errorf("Expected Content-Type application/x-ndjson, got %q", ct)
		}

		// Parse NDJSON response
		lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
		if len(lines) != 3 {
			t.Errorf("Expected 3 lines, got %d: %s", len(lines), rec.Body.String())
			return
		}

		for i, line := range lines {
			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				t.Errorf("Failed to parse line %d: %v", i, err)
				continue
			}

			expectedID := 100 + i
			if int(msg["id"].(float64)) != expectedID {
				t.Errorf("Line %d: expected id=%d, got %v", i, expectedID, msg["id"])
			}

			expectedData := fmt.Sprintf("server:hello:%d", i)
			if msg["data"] != expectedData {
				t.Errorf("Line %d: expected data=%q, got %v", i, expectedData, msg["data"])
			}
		}
	})
}

func TestClientStreamingIntegration(t *testing.T) {
	server := startTestStreamServer(t)
	defer server.Stop()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-client-stream",
			Address:  ":0",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 30 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:         "client-stream-test",
			Path:       "/api/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://" + server.Address(),
			}},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "streamtest.StreamService",
					Timeout: 10 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	t.Run("client streaming accepts NDJSON", func(t *testing.T) {
		// Send NDJSON request body
		ndjsonBody := `{"id":1,"data":"msg1"}
{"id":2,"data":"msg2"}
{"id":3,"data":"msg3"}
`
		req := httptest.NewRequest(http.MethodPost, "/api/ClientStream", strings.NewReader(ndjsonBody))
		req.Header.Set("Content-Type", "application/x-ndjson")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
			return
		}

		// Client streaming returns single JSON (not NDJSON)
		ct := rec.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %q", ct)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v, body: %s", err, rec.Body.String())
		}

		idVal, ok := resp["id"]
		if !ok || idVal == nil {
			t.Fatalf("Missing id field in response: %v", resp)
		}
		if int(idVal.(float64)) != 3 {
			t.Errorf("Expected id=3 (count), got %v", idVal)
		}

		expectedData := "client:received:3:last:msg3"
		if resp["data"] != expectedData {
			t.Errorf("Expected data=%q, got %v", expectedData, resp["data"])
		}
	})
}

func TestBidirectionalStreamingIntegration(t *testing.T) {
	server := startTestStreamServer(t)
	defer server.Stop()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-bidi-stream",
			Address:  ":0",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 30 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:         "bidi-stream-test",
			Path:       "/api/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://" + server.Address(),
			}},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "streamtest.StreamService",
					Timeout: 10 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	t.Run("bidi streaming echo", func(t *testing.T) {
		// Send NDJSON request body
		ndjsonBody := `{"id":1,"data":"alpha"}
{"id":2,"data":"beta"}
`
		req := httptest.NewRequest(http.MethodPost, "/api/BidiStream", strings.NewReader(ndjsonBody))
		req.Header.Set("Content-Type", "application/x-ndjson")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
			return
		}

		// Check content type
		ct := rec.Header().Get("Content-Type")
		if ct != "application/x-ndjson" {
			t.Errorf("Expected Content-Type application/x-ndjson, got %q", ct)
		}

		// Parse NDJSON response
		lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
		if len(lines) != 2 {
			t.Errorf("Expected 2 lines, got %d: %s", len(lines), rec.Body.String())
			return
		}

		expectedData := []string{"bidi:alpha", "bidi:beta"}
		for i, line := range lines {
			var msg map[string]interface{}
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				t.Errorf("Failed to parse line %d: %v", i, err)
				continue
			}

			if msg["data"] != expectedData[i] {
				t.Errorf("Line %d: expected data=%q, got %v", i, expectedData[i], msg["data"])
			}
		}
	})
}

func TestUnaryStillWorks(t *testing.T) {
	server := startTestStreamServer(t)
	defer server.Stop()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-unary",
			Address:  ":0",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 30 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:         "unary-test",
			Path:       "/api/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://" + server.Address(),
			}},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "streamtest.StreamService",
					Timeout: 10 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	t.Run("unary still works after streaming support", func(t *testing.T) {
		body := `{"id":42,"data":"test"}`
		req := httptest.NewRequest(http.MethodPost, "/api/Unary", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d: %s", rec.Code, rec.Body.String())
			return
		}

		// Unary returns regular JSON
		ct := rec.Header().Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %q", ct)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Failed to parse response: %v", err)
		}

		if int(resp["id"].(float64)) != 42 {
			t.Errorf("Expected id=42, got %v", resp["id"])
		}

		if resp["data"] != "unary:test" {
			t.Errorf("Expected data='unary:test', got %v", resp["data"])
		}
	})
}

func TestStreamingError(t *testing.T) {
	// Test that errors are handled correctly during streaming

	// Create a custom gRPC server that returns an error mid-stream
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port

	files, msgDesc := buildStreamingServiceDescriptor(t)

	s := grpc.NewServer()

	// Register error-producing service
	idField := msgDesc.Fields().ByName("id")
	dataField := msgDesc.Fields().ByName("data")

	errorStreamHandler := func(_ interface{}, stream grpc.ServerStream) error {
		msg := dynamicpb.NewMessage(msgDesc)
		if err := stream.RecvMsg(msg); err != nil {
			return err
		}

		// Send one message then error
		resp := dynamicpb.NewMessage(msgDesc)
		resp.Set(idField, protoreflect.ValueOfInt32(1))
		resp.Set(dataField, protoreflect.ValueOfString("first"))
		if err := stream.SendMsg(resp); err != nil {
			return err
		}

		return status.Error(codes.Internal, "simulated error")
	}

	svcDesc := grpc.ServiceDesc{
		ServiceName: "streamtest.StreamService",
		Streams: []grpc.StreamDesc{
			{
				StreamName:    "ServerStream",
				Handler:       errorStreamHandler,
				ServerStreams: true,
			},
		},
	}
	s.RegisterService(&svcDesc, nil)

	// Register custom reflection service
	registerCustomReflection(s, files)

	go s.Serve(lis)
	defer func() {
		s.Stop()
		lis.Close()
	}()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-error",
			Address:  ":0",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 30 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:         "error-test",
			Path:       "/api/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: fmt.Sprintf("grpc://127.0.0.1:%d", port),
			}},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "streamtest.StreamService",
					Timeout: 10 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	t.Run("error mid-stream appears as final NDJSON line", func(t *testing.T) {
		body := `{"id":1,"data":"trigger"}`
		req := httptest.NewRequest(http.MethodPost, "/api/ServerStream", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		// We get 200 initially because streaming has started
		// Error appears in the body

		lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
		if len(lines) < 1 {
			t.Errorf("Expected at least 1 line, got: %s", rec.Body.String())
			return
		}

		// Last line should be error
		lastLine := lines[len(lines)-1]
		if !strings.Contains(lastLine, `"error"`) {
			t.Errorf("Expected last line to be error, got: %s", lastLine)
		}
		if !strings.Contains(lastLine, "simulated error") {
			t.Errorf("Expected error message, got: %s", lastLine)
		}
	})
}
