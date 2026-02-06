package grpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	rpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// cachedDescriptors holds cached service descriptors for a backend.
type cachedDescriptors struct {
	services  map[string]protoreflect.ServiceDescriptor
	expiresAt time.Time
	mu        sync.RWMutex
}

// descriptorCache manages descriptor caching for backends.
type descriptorCache struct {
	cache sync.Map // backend URL → *cachedDescriptors
	ttl   time.Duration
}

func newDescriptorCache(ttl time.Duration) *descriptorCache {
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	return &descriptorCache{ttl: ttl}
}

// getServiceDescriptor retrieves a service descriptor, fetching from the backend if not cached.
func (dc *descriptorCache) getServiceDescriptor(ctx context.Context, conn *grpc.ClientConn, backendURL, serviceName string) (protoreflect.ServiceDescriptor, error) {
	// Check cache
	if cached, ok := dc.cache.Load(backendURL); ok {
		cd := cached.(*cachedDescriptors)
		cd.mu.RLock()
		if time.Now().Before(cd.expiresAt) {
			if sd, exists := cd.services[serviceName]; exists {
				cd.mu.RUnlock()
				return sd, nil
			}
		}
		cd.mu.RUnlock()
	}

	// Fetch from backend via reflection
	services, err := dc.fetchAllServices(ctx, conn)
	if err != nil {
		return nil, err
	}

	// Store in cache
	cd := &cachedDescriptors{
		services:  services,
		expiresAt: time.Now().Add(dc.ttl),
	}
	dc.cache.Store(backendURL, cd)

	if sd, ok := services[serviceName]; ok {
		return sd, nil
	}
	return nil, fmt.Errorf("service %q not found on backend %s", serviceName, backendURL)
}

// fetchAllServices uses gRPC reflection to fetch all service descriptors.
func (dc *descriptorCache) fetchAllServices(ctx context.Context, conn *grpc.ClientConn) (map[string]protoreflect.ServiceDescriptor, error) {
	client := rpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create reflection stream: %w", err)
	}
	defer stream.CloseSend()

	// List all services
	if err := stream.Send(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_ListServices{},
	}); err != nil {
		return nil, fmt.Errorf("failed to send list services request: %w", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("failed to receive list services response: %w", err)
	}

	listResp, ok := resp.MessageResponse.(*rpb.ServerReflectionResponse_ListServicesResponse)
	if !ok {
		return nil, fmt.Errorf("unexpected response type for list services")
	}

	services := make(map[string]protoreflect.ServiceDescriptor)
	seenFiles := make(map[string]bool)
	var fileDescProtos []*descriptorpb.FileDescriptorProto

	// Fetch file descriptors for each service
	for _, svc := range listResp.ListServicesResponse.Service {
		if svc.Name == "grpc.reflection.v1alpha.ServerReflection" || svc.Name == "grpc.reflection.v1.ServerReflection" {
			continue
		}

		if err := stream.Send(&rpb.ServerReflectionRequest{
			MessageRequest: &rpb.ServerReflectionRequest_FileContainingSymbol{
				FileContainingSymbol: svc.Name,
			},
		}); err != nil {
			return nil, fmt.Errorf("failed to send file request for %s: %w", svc.Name, err)
		}

		resp, err := stream.Recv()
		if err != nil {
			return nil, fmt.Errorf("failed to receive file descriptor for %s: %w", svc.Name, err)
		}

		fdResp, ok := resp.MessageResponse.(*rpb.ServerReflectionResponse_FileDescriptorResponse)
		if !ok {
			continue
		}

		for _, fdBytes := range fdResp.FileDescriptorResponse.FileDescriptorProto {
			fdProto := &descriptorpb.FileDescriptorProto{}
			if err := proto.Unmarshal(fdBytes, fdProto); err != nil {
				continue
			}
			if !seenFiles[fdProto.GetName()] {
				seenFiles[fdProto.GetName()] = true
				fileDescProtos = append(fileDescProtos, fdProto)
			}
		}
	}

	// Build file descriptors
	files, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: fileDescProtos})
	if err != nil {
		return nil, fmt.Errorf("failed to build file descriptors: %w", err)
	}

	// Extract service descriptors
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for i := 0; i < fd.Services().Len(); i++ {
			sd := fd.Services().Get(i)
			services[string(sd.FullName())] = sd
		}
		return true
	})

	return services, nil
}

// getMethodDescriptor finds a method descriptor within a service.
func getMethodDescriptor(sd protoreflect.ServiceDescriptor, methodName string) (protoreflect.MethodDescriptor, error) {
	methods := sd.Methods()
	for i := 0; i < methods.Len(); i++ {
		md := methods.Get(i)
		if string(md.Name()) == methodName {
			return md, nil
		}
	}
	return nil, fmt.Errorf("method %q not found in service %q", methodName, sd.FullName())
}

// resolveTypes builds a local type registry from a file descriptor.
func resolveTypes(fd protoreflect.FileDescriptor) *protoregistry.Types {
	types := new(protoregistry.Types)
	registerTypesFromFile(types, fd)
	return types
}

func registerTypesFromFile(types *protoregistry.Types, fd protoreflect.FileDescriptor) {
	// Register all messages
	msgs := fd.Messages()
	for i := 0; i < msgs.Len(); i++ {
		registerMessageType(types, msgs.Get(i))
	}

	// Register messages from dependencies
	imports := fd.Imports()
	for i := 0; i < imports.Len(); i++ {
		registerTypesFromFile(types, imports.Get(i).FileDescriptor)
	}
}

func registerMessageType(types *protoregistry.Types, md protoreflect.MessageDescriptor) {
	// Skip if already registered
	if _, err := types.FindMessageByName(md.FullName()); err == nil {
		return
	}

	// Create and register the message type
	mt := dynamicMessageType{md}
	types.RegisterMessage(mt)

	// Register nested messages
	nested := md.Messages()
	for i := 0; i < nested.Len(); i++ {
		registerMessageType(types, nested.Get(i))
	}
}

// dynamicMessageType implements protoreflect.MessageType for dynamic messages.
type dynamicMessageType struct {
	desc protoreflect.MessageDescriptor
}

func (t dynamicMessageType) New() protoreflect.Message {
	return nil // Not used for our purposes
}

func (t dynamicMessageType) Zero() protoreflect.Message {
	return nil
}

func (t dynamicMessageType) Descriptor() protoreflect.MessageDescriptor {
	return t.desc
}
