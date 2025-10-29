package mcpmgr

// Lightweight helpers for narrowing and inspecting ServerConfig values without
// forcing consumers to use a type switch at every call site. These are
// non-breaking and purely additive.

// ConfigTransport identifies the transport family used by a ServerConfig.
type ConfigTransport string

const (
    TransportStdio ConfigTransport = "stdio"
    TransportHTTP  ConfigTransport = "http"
)

// TransportOf returns the transport kind for a ServerConfig.
// Returns an empty string when the value is nil or an unknown implementation.
func TransportOf(cfg ServerConfig) ConfigTransport {
    switch cfg.(type) {
    case *StdioServerConfig:
        return TransportStdio
    case *HTTPServerConfig:
        return TransportHTTP
    default:
        return ""
    }
}

// IsStdio reports whether cfg is a *StdioServerConfig.
func IsStdio(cfg ServerConfig) bool {
    _, ok := cfg.(*StdioServerConfig)
    return ok
}

// IsHTTP reports whether cfg is a *HTTPServerConfig.
func IsHTTP(cfg ServerConfig) bool {
    _, ok := cfg.(*HTTPServerConfig)
    return ok
}

// AsStdio narrows cfg to *StdioServerConfig, returning (nil, false) when it
// does not match.
func AsStdio(cfg ServerConfig) (*StdioServerConfig, bool) {
    c, ok := cfg.(*StdioServerConfig)
    return c, ok
}

// AsHTTP narrows cfg to *HTTPServerConfig, returning (nil, false) when it
// does not match.
func AsHTTP(cfg ServerConfig) (*HTTPServerConfig, bool) {
    c, ok := cfg.(*HTTPServerConfig)
    return c, ok
}

