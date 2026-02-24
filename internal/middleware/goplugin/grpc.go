package goplugin

import (
	"fmt"
	"net/rpc"

	"github.com/hashicorp/go-plugin"
)

// MakeHandshake creates a handshake config with the given key.
func MakeHandshake(key string) plugin.HandshakeConfig {
	if key == "" {
		key = "gateway-v1"
	}
	return plugin.HandshakeConfig{
		ProtocolVersion:  1,
		MagicCookieKey:   "GATEWAY_PLUGIN",
		MagicCookieValue: key,
	}
}

// MakePluginMap creates a plugin map for go-plugin.
func MakePluginMap() map[string]plugin.Plugin {
	return map[string]plugin.Plugin{
		"gateway": &GatewayRPCPlugin{},
	}
}

// GatewayRPCPlugin implements plugin.Plugin using net/rpc.
type GatewayRPCPlugin struct {
	Impl GatewayPlugin
}

// Server returns the RPC server (plugin side).
func (p *GatewayRPCPlugin) Server(*plugin.MuxBroker) (interface{}, error) {
	return &RPCServer{Impl: p.Impl}, nil
}

// Client returns the RPC client (host side).
func (p *GatewayRPCPlugin) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &RPCClient{client: c}, nil
}

// --- RPC types ---

// InitArgs is the argument to the Init RPC call.
type InitArgs struct {
	Config map[string]string
}

// InitReply is the reply from the Init RPC call.
type InitReply struct {
	Error string
}

// OnRequestArgs is the argument to the OnRequest RPC call.
type OnRequestArgs struct {
	Request PluginRequest
}

// OnResponseArgs is the argument to the OnResponse RPC call.
type OnResponseArgs struct {
	Request         PluginRequest
	StatusCode      int
	ResponseHeaders map[string]string
	ResponseBody    []byte
}

// --- RPC Server (plugin process side) ---

// RPCServer wraps GatewayPlugin for the RPC server side.
type RPCServer struct {
	Impl GatewayPlugin
}

func (s *RPCServer) Init(args *InitArgs, reply *InitReply) error {
	err := s.Impl.Init(args.Config)
	if err != nil {
		reply.Error = err.Error()
	}
	return nil
}

func (s *RPCServer) OnRequest(args *OnRequestArgs, reply *PluginResponse) error {
	*reply = s.Impl.OnRequest(args.Request)
	return nil
}

func (s *RPCServer) OnResponse(args *OnResponseArgs, reply *PluginResponse) error {
	*reply = s.Impl.OnResponse(args.Request, args.StatusCode, args.ResponseHeaders, args.ResponseBody)
	return nil
}

// --- RPC Client (host process side) ---

// RPCClient wraps the RPC client to implement GatewayPlugin.
type RPCClient struct {
	client *rpc.Client
}

func (c *RPCClient) Init(config map[string]string) error {
	var reply InitReply
	err := c.client.Call("Plugin.Init", &InitArgs{Config: config}, &reply)
	if err != nil {
		return err
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}

func (c *RPCClient) OnRequest(req PluginRequest) PluginResponse {
	var reply PluginResponse
	err := c.client.Call("Plugin.OnRequest", &OnRequestArgs{Request: req}, &reply)
	if err != nil {
		return PluginResponse{Action: "continue"}
	}
	return reply
}

func (c *RPCClient) OnResponse(req PluginRequest, statusCode int, respHeaders map[string]string, respBody []byte) PluginResponse {
	var reply PluginResponse
	err := c.client.Call("Plugin.OnResponse", &OnResponseArgs{
		Request:         req,
		StatusCode:      statusCode,
		ResponseHeaders: respHeaders,
		ResponseBody:    respBody,
	}, &reply)
	if err != nil {
		return PluginResponse{Action: "continue"}
	}
	return reply
}
