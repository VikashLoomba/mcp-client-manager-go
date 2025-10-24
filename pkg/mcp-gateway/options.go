package mcpgateway

import (
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configure a Gateway instance.
type Options struct {
	// Implementation identifies the gateway's MCP server implementation metadata.
	Implementation *mcp.Implementation
	// Addr controls the listen address used by ListenAndServe. Defaults to ":8700".
	Addr string
	// Path optionally mounts the Streamable handler under a specific HTTP path.
	// Defaults to "/mcp".
	Path string
	// Namespace customizes how upstream names and URIs are exposed to downstream
	// clients. Defaults to ServerPrefixNamespace.
	Namespace NamespaceStrategy
	// AutoConnect eagerly dial all configured mcpmgr servers during construction.
	AutoConnect bool
	// Streamable tweaks the Streamable HTTP handler behavior passed to
	// mcp.NewStreamableHTTPHandler.
	Streamable mcp.StreamableHTTPOptions
	// Logger receives structured diagnostics.
	Logger *slog.Logger
	// SyncTimeout bounds how long initial and incremental synchronizations may take.
	SyncTimeout time.Duration
}

func (o *Options) withDefaults() Options {
	if o == nil {
		o = &Options{}
	}
	opts := *o
	if opts.Implementation == nil {
		opts.Implementation = &mcp.Implementation{
			Name:    "mcpgateway",
			Title:   "MCP Gateway",
			Version: "1.0.0",
		}
	} else {
		impl := *opts.Implementation
		opts.Implementation = &impl
	}
	if opts.Addr == "" {
		opts.Addr = ":8700"
	}
	if opts.Path == "" {
		opts.Path = "/mcp"
	}
	if opts.Namespace == nil {
		opts.Namespace = ServerPrefixNamespace{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SyncTimeout <= 0 {
		opts.SyncTimeout = 30 * time.Second
	}
	return opts
}
