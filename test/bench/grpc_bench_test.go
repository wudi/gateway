//go:build integration

package bench

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"google.golang.org/grpc"
	rpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	// Register the grpc translator for http_to_grpc tests
	_ "github.com/wudi/runway/internal/proxy/protocol/grpc"
)

// benchGRPCServer is a gRPC server for benchmark tests.
type benchGRPCServer struct {
	listener   net.Listener
	tracker    *connTracker
	grpcServer *grpc.Server
	files      *protoregistry.Files
	msgDesc    protoreflect.MessageDescriptor
}

func startBenchGRPCServer(tb testing.TB) *benchGRPCServer {
	tb.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("failed to listen: %v", err)
	}
	tracker := newConnTracker(lis)

	files, msgDesc := buildEchoServiceDescriptor(tb)

	s := grpc.NewServer()
	registerEchoService(s, files, msgDesc)
	registerBenchReflection(s, files)

	go func() {
		if err := s.Serve(tracker); err != nil && err != grpc.ErrServerStopped {
			tb.Logf("grpc server error: %v", err)
		}
	}()

	tb.Cleanup(func() {
		s.GracefulStop()
		lis.Close()
	})

	return &benchGRPCServer{
		listener:   lis,
		tracker:    tracker,
		grpcServer: s,
		files:      files,
		msgDesc:    msgDesc,
	}
}

func (s *benchGRPCServer) Address() string {
	return s.listener.Addr().String()
}

// buildEchoServiceDescriptor creates a proto descriptor for a simple Echo service.
func buildEchoServiceDescriptor(tb testing.TB) (*protoregistry.Files, protoreflect.MessageDescriptor) {
	tb.Helper()

	fileDesc := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("bench_echo.proto"),
		Package: proto.String("benchtest"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("EchoMessage"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("id"),
						Number: proto.Int32(1),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					},
					{
						Name:   proto.String("message"),
						Number: proto.Int32(2),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
					},
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("EchoService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("Echo"),
						InputType:  proto.String(".benchtest.EchoMessage"),
						OutputType: proto.String(".benchtest.EchoMessage"),
					},
				},
			},
		},
	}

	files, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{fileDesc},
	})
	if err != nil {
		tb.Fatalf("failed to build file descriptors: %v", err)
	}

	fd, err := files.FindFileByPath("bench_echo.proto")
	if err != nil {
		tb.Fatalf("failed to find file: %v", err)
	}

	return files, fd.Messages().Get(0)
}

// registerEchoService registers the EchoService unary handler.
func registerEchoService(s *grpc.Server, files *protoregistry.Files, msgDesc protoreflect.MessageDescriptor) {
	fd, _ := files.FindFileByPath("bench_echo.proto")
	sd := fd.Services().Get(0)

	svcDesc := grpc.ServiceDesc{
		ServiceName: string(sd.FullName()),
		HandlerType: nil,
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Echo",
				Handler: func(_ interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
					msg := dynamicpb.NewMessage(msgDesc)
					if err := dec(msg); err != nil {
						return nil, err
					}
					resp := dynamicpb.NewMessage(msgDesc)
					resp.Set(msgDesc.Fields().ByName("id"), msg.Get(msgDesc.Fields().ByName("id")))
					resp.Set(msgDesc.Fields().ByName("message"), msg.Get(msgDesc.Fields().ByName("message")))
					return resp, nil
				},
			},
		},
	}
	s.RegisterService(&svcDesc, nil)
}

// registerBenchReflection registers a reflection service for the dynamic descriptors.
func registerBenchReflection(s *grpc.Server, files *protoregistry.Files) {
	rpb.RegisterServerReflectionServer(s, &benchReflectionServer{files: files})
}

type benchReflectionServer struct {
	rpb.UnimplementedServerReflectionServer
	files *protoregistry.Files
}

func (s *benchReflectionServer) ServerReflectionInfo(stream rpb.ServerReflection_ServerReflectionInfoServer) error {
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		resp := &rpb.ServerReflectionResponse{
			OriginalRequest: req,
		}

		switch r := req.MessageRequest.(type) {
		case *rpb.ServerReflectionRequest_ListServices:
			services := &rpb.ListServiceResponse{}
			s.files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
				for i := 0; i < fd.Services().Len(); i++ {
					services.Service = append(services.Service, &rpb.ServiceResponse{
						Name: string(fd.Services().Get(i).FullName()),
					})
				}
				return true
			})
			resp.MessageResponse = &rpb.ServerReflectionResponse_ListServicesResponse{
				ListServicesResponse: services,
			}
		case *rpb.ServerReflectionRequest_FileContainingSymbol:
			desc, err := s.files.FindDescriptorByName(protoreflect.FullName(r.FileContainingSymbol))
			if err != nil {
				resp.MessageResponse = &rpb.ServerReflectionResponse_ErrorResponse{
					ErrorResponse: &rpb.ErrorResponse{ErrorMessage: err.Error()},
				}
			} else {
				var fd protoreflect.FileDescriptor
				switch d := desc.(type) {
				case protoreflect.ServiceDescriptor:
					fd = d.ParentFile()
				case protoreflect.MessageDescriptor:
					fd = d.ParentFile()
				default:
					resp.MessageResponse = &rpb.ServerReflectionResponse_ErrorResponse{
						ErrorResponse: &rpb.ErrorResponse{ErrorMessage: "unsupported descriptor type"},
					}
					stream.Send(resp)
					continue
				}
				fdProto := protodesc.ToFileDescriptorProto(fd)
				b, _ := proto.Marshal(fdProto)
				resp.MessageResponse = &rpb.ServerReflectionResponse_FileDescriptorResponse{
					FileDescriptorResponse: &rpb.FileDescriptorResponse{
						FileDescriptorProto: [][]byte{b},
					},
				}
			}
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
	}
}

// --- gRPC Translation Benchmarks (HTTP JSON → Runway → gRPC backend) ---

func BenchmarkGRPCTranslation(b *testing.B) {
	server := startBenchGRPCServer(b)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "grpc-translate-bench",
		Path:       "/api/*method",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: "grpc://" + server.Address(),
		}},
		Protocol: config.ProtocolConfig{
			Type: "http_to_grpc",
			GRPC: config.GRPCTranslateConfig{
				Service: "benchtest.EchoService",
				Timeout: 10 * time.Second,
			},
		},
	}}

	handler, cleanup := newTestRunway(b, cfg)
	defer cleanup()

	// Warm up: first request triggers reflection + descriptor caching.
	warmReq := httptest.NewRequest(http.MethodPost, "/api/Echo", strings.NewReader(`{"id":0,"message":"warmup"}`))
	warmReq.Header.Set("Content-Type", "application/json")
	warmRec := httptest.NewRecorder()
	handler.ServeHTTP(warmRec, warmReq)
	if warmRec.Code != http.StatusOK {
		b.Fatalf("warmup failed: %d %s", warmRec.Code, warmRec.Body.String())
	}

	b.Run("Serial", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			body := fmt.Sprintf(`{"id":%d,"message":"hello"}`, i)
			req := httptest.NewRequest(http.MethodPost, "/api/Echo", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
		}
	})

	b.Run("Parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				body := fmt.Sprintf(`{"id":%d,"message":"hello"}`, i)
				req := httptest.NewRequest(http.MethodPost, "/api/Echo", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					b.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
				}
				i++
			}
		})
	})
}

func TestGRPCTranslationConnectionReuse(t *testing.T) {
	server := startBenchGRPCServer(t)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "grpc-translate-connreuse",
		Path:       "/api/*method",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: "grpc://" + server.Address(),
		}},
		Protocol: config.ProtocolConfig{
			Type: "http_to_grpc",
			GRPC: config.GRPCTranslateConfig{
				Service: "benchtest.EchoService",
				Timeout: 10 * time.Second,
			},
		},
	}}

	handler, cleanup := newTestRunway(t, cfg)
	defer cleanup()

	// Send 100 requests through the translator.
	for i := 0; i < 100; i++ {
		body := fmt.Sprintf(`{"id":%d,"message":"hello"}`, i)
		req := httptest.NewRequest(http.MethodPost, "/api/Echo", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d: %s", i, rec.Code, rec.Body.String())
		}
	}

	// The gRPC translator maintains a single *grpc.ClientConn per backend
	// with HTTP/2 multiplexing — expect 1 TCP connection.
	conns := server.tracker.UniqueConns()
	t.Logf("gRPC translation: 100 requests used %d unique connections", conns)
	if conns > 2 {
		t.Errorf("expected <= 2 unique connections (HTTP/2 multiplexing), got %d", conns)
	}
}

// --- Parallel connection reuse test for gRPC translation ---

func TestGRPCTranslationConnectionReuse_Parallel(t *testing.T) {
	server := startBenchGRPCServer(t)

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:         "grpc-translate-connreuse-par",
		Path:       "/api/*method",
		PathPrefix: true,
		Backends: []config.BackendConfig{{
			URL: "grpc://" + server.Address(),
		}},
		Protocol: config.ProtocolConfig{
			Type: "http_to_grpc",
			GRPC: config.GRPCTranslateConfig{
				Service: "benchtest.EchoService",
				Timeout: 10 * time.Second,
			},
		},
	}}

	handler, cleanup := newTestRunway(t, cfg)
	defer cleanup()

	// Warmup.
	warmReq := httptest.NewRequest(http.MethodPost, "/api/Echo", strings.NewReader(`{"id":0,"message":"warmup"}`))
	warmReq.Header.Set("Content-Type", "application/json")
	warmRec := httptest.NewRecorder()
	handler.ServeHTTP(warmRec, warmReq)
	if warmRec.Code != http.StatusOK {
		t.Fatalf("warmup failed: %d %s", warmRec.Code, warmRec.Body.String())
	}

	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				body := fmt.Sprintf(`{"id":%d,"message":"hello"}`, i)
				req := httptest.NewRequest(http.MethodPost, "/api/Echo", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
				}
			}
		}()
	}
	wg.Wait()

	conns := server.tracker.UniqueConns()
	t.Logf("gRPC translation parallel: 100 requests (10x10) used %d unique connections", conns)
	// Single grpc.ClientConn with HTTP/2 multiplexing — should be very few connections.
	if conns > 3 {
		t.Errorf("expected <= 3 unique connections (HTTP/2 multiplexing), got %d", conns)
	}
}

