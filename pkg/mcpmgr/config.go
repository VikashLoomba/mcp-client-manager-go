package mcpmgr

import (
	"context"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RPCDirection represents the direction of an observed JSON-RPC message.
type RPCDirection string

const (
	RPCDirectionSend    RPCDirection = "send"
	RPCDirectionReceive RPCDirection = "receive"
)

// RPCLogEvent encapsulates JSON-RPC traffic for custom logging.
type RPCLogEvent struct {
	Direction RPCDirection
	Message   []byte
	ServerID  string
}

// RPCLogger is invoked for each JSON-RPC message when logging is enabled.
type RPCLogger func(RPCLogEvent)

// HTTPAuthProvider dynamically supplies an Authorization header (for example,
// "Bearer <token>") for outbound HTTP requests initiated by the manager.
type HTTPAuthProvider func(context.Context) (string, error)

// HTTPRequestInit mirrors the Streamable HTTP requestInit options exposed by
// the TypeScript SDK. Only headers are currently supported.
type HTTPRequestInit struct {
	Headers http.Header
}

// SSERequestInit mirrors the SSE eventSourceInit options exposed by the
// TypeScript SDK. Only headers are currently supported.
type SSERequestInit struct {
	Headers http.Header
}

// StreamableReconnectionOptions configures the reconnect strategy for the
// Streamable HTTP transport.
type StreamableReconnectionOptions struct {
	MaxRetries int
}

// BaseServerConfig captures settings shared by all transport types.
type BaseServerConfig struct {
	ClientOptions mcp.ClientOptions
	Timeout       time.Duration
	Version       string
	OnError       func(error)
	LogJSONRPC    bool
	RPCLogger     RPCLogger
}

// StdioServerConfig describes an MCP server launched via stdio.
type StdioServerConfig struct {
	BaseServerConfig
	Command string
	Args    []string
	Env     map[string]string
}

func (c *StdioServerConfig) base() *BaseServerConfig { return &c.BaseServerConfig }

// HTTPServerConfig describes an MCP server reachable over HTTP transports.
type HTTPServerConfig struct {
	BaseServerConfig
	Endpoint   string
	HTTPClient *http.Client
	MaxRetries int

	RequestInit         *HTTPRequestInit
	EventSourceInit     *SSERequestInit
	AuthProvider        HTTPAuthProvider
	ReconnectionOptions *StreamableReconnectionOptions
	SessionID           string
	PreferSSE           *bool
}

func (c *HTTPServerConfig) base() *BaseServerConfig { return &c.BaseServerConfig }

// ServerConfig is implemented by all transport-specific configurations.
type ServerConfig interface {
	base() *BaseServerConfig
}

// ManagerOptions configures a Manager instance.
type ManagerOptions struct {
	// DefaultClientName overrides the client name advertised during
	// initialization. When empty, the server ID is used.
	DefaultClientName string
	// DefaultClientVersion controls the semantic version reported to servers.
	DefaultClientVersion string
	// DefaultTimeout is applied whenever a server configuration omits an
	// explicit timeout.
	DefaultTimeout time.Duration
	// DefaultClientOptions are merged into each server's BaseServerConfig
	// options prior to connection.
	DefaultClientOptions mcp.ClientOptions
	// DefaultLogJSONRPC toggles console logging of JSON-RPC traffic for all
	// servers unless overridden per server.
	DefaultLogJSONRPC bool
	// RPCLogger provides a custom logger for JSON-RPC traffic; it takes
	// precedence over DefaultLogJSONRPC.
	RPCLogger RPCLogger
	// AutoConnect instructs the manager to dial all configured servers in the
	// background immediately after construction.
	AutoConnect bool
}

func (o *ManagerOptions) normalized() ManagerOptions {
	if o == nil {
		return ManagerOptions{}
	}
	return *o
}
