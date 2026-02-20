package rest

import (
	"fmt"
	"os"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// descriptorRegistry holds loaded protobuf type descriptors for message encoding/decoding.
type descriptorRegistry struct {
	mu       sync.RWMutex
	files    *protoregistry.Files
	services map[string]protoreflect.ServiceDescriptor // keyed by full service name
}

// newDescriptorRegistry creates a registry and loads descriptor files.
func newDescriptorRegistry(descriptorFiles []string) (*descriptorRegistry, error) {
	reg := &descriptorRegistry{
		files:    new(protoregistry.Files),
		services: make(map[string]protoreflect.ServiceDescriptor),
	}

	for _, path := range descriptorFiles {
		if err := reg.loadFile(path); err != nil {
			return nil, fmt.Errorf("loading descriptor %s: %w", path, err)
		}
	}

	return reg, nil
}

// loadFile loads a pre-compiled .pb descriptor set file.
func (r *descriptorRegistry) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(data, fds); err != nil {
		return fmt.Errorf("unmarshal descriptor set: %w", err)
	}

	for _, fd := range fds.GetFile() {
		fileDesc, err := protodesc.NewFile(fd, r.files)
		if err != nil {
			// File may already be registered (from include_imports), skip
			continue
		}
		if err := r.files.RegisterFile(fileDesc); err != nil {
			// Already registered, skip
			continue
		}

		// Index services
		for i := 0; i < fileDesc.Services().Len(); i++ {
			svc := fileDesc.Services().Get(i)
			r.services[string(svc.FullName())] = svc
		}
	}

	return nil
}

// findMethod looks up a method descriptor by service and method name.
func (r *descriptorRegistry) findMethod(service, method string) (protoreflect.MethodDescriptor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	svc, ok := r.services[service]
	if !ok {
		return nil, fmt.Errorf("service %q not found in descriptors", service)
	}

	md := svc.Methods().ByName(protoreflect.Name(method))
	if md == nil {
		return nil, fmt.Errorf("method %q not found in service %q", method, service)
	}

	return md, nil
}

// newInputMessage creates a new dynamic message for the method's input type.
func (r *descriptorRegistry) newInputMessage(md protoreflect.MethodDescriptor) *dynamicpb.Message {
	return dynamicpb.NewMessage(md.Input())
}

// newOutputMessage creates a new dynamic message for the method's output type.
func (r *descriptorRegistry) newOutputMessage(md protoreflect.MethodDescriptor) *dynamicpb.Message {
	return dynamicpb.NewMessage(md.Output())
}
