package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	mcpgateway "github.com/vikashloomba/mcp-client-manager-go/pkg/mcp-gateway"
	"github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

type McpService struct {
	// Your service fields
	gateway *mcpgateway.Gateway
	manager *mcpmgr.Manager
}

// SerializedServerSummary exposes a JSON-friendly representation of the manager's
// ServerSummary for UI consumption.
type SerializedServerSummary struct {
	ID     string                  `json:"id"`
	Status mcpmgr.ConnectionStatus `json:"status"`
	Config *SerializedServerConfig `json:"config,omitempty"`
}

// SerializedServerConfig provides a transport-agnostic view of a ServerConfig.
type SerializedServerConfig struct {
	Type           string            `json:"type"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Endpoint       string            `json:"endpoint,omitempty"`
	MaxRetries     *int              `json:"maxRetries,omitempty"`
	SessionID      string            `json:"sessionId,omitempty"`
	PreferSSE      *bool             `json:"preferSse,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
	Version        string            `json:"version,omitempty"`
	LogJSONRPC     bool              `json:"logJsonRpc"`
}

func NewMcpService() *McpService {
	// Initialize and return your service
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	stdioConfig := &mcpmgr.StdioServerConfig{
		BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 15 * time.Second},
		Command:          "npx",
		Args:             []string{"@modelcontextprotocol/server-everything"},
	}
	manager := mcpmgr.NewManager(nil, &mcpmgr.ManagerOptions{DefaultClientName: "gateway-example"})
	gatewayOpts := &mcpgateway.Options{
		Addr:        ":8787",
		Path:        "/mcp",
		AutoConnect: true,
		Streamable: mcp.StreamableHTTPOptions{
			Stateless:    false,
			JSONResponse: true,
		},
	}
	gateway, err := mcpgateway.NewGateway(manager, gatewayOpts)
	if err != nil {
		log.Fatalf("failed to build gateway: %v", err)
	}
	if err := gateway.AttachServer(ctx, "stdio-example", stdioConfig); err != nil {
		log.Fatalf("failed to attach stdio-example server: %v", err)
	}
	gwOptions := gateway.Options()
	log.Printf("gateway serving Streamable MCP on %s%s", gwOptions.Addr, gwOptions.Path)
	// Run the gateway server in a separate goroutine so it doesn't block.
	go func() {
		err := gateway.ListenAndServe(ctx)
		// Release signal resources regardless of outcome.
		stop()
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("gateway server stopped: %v", err)
		}
	}()
	return &McpService{
		gateway: gateway,
		manager: manager,
	}
}

func (s *McpService) GetServers() []string {
	return s.manager.ListServers()
}

func (s *McpService) GetServersWithDetails() []SerializedServerSummary {
	summaries := s.manager.GetServerSummaries()
	result := make([]SerializedServerSummary, 0, len(summaries))
	for _, summary := range summaries {
		serialized := SerializedServerSummary{
			ID:     summary.ID,
			Status: summary.Status,
			Config: serializeServerConfig(summary.Config),
		}
		result = append(result, serialized)
	}
	return result
}

func serializeServerConfig(cfg mcpmgr.ServerConfig) *SerializedServerConfig {
	if cfg == nil {
		return nil
	}
	switch mcpmgr.TransportOf(cfg) {
	case mcpmgr.TransportStdio:
		if stdioCfg, ok := mcpmgr.AsStdio(cfg); ok && stdioCfg != nil {
			return &SerializedServerConfig{
				Type:           string(mcpmgr.TransportStdio),
				Command:        stdioCfg.Command,
				Args:           cloneStringSlice(stdioCfg.Args),
				Env:            cloneStringMap(stdioCfg.Env),
				TimeoutSeconds: durationToSeconds(stdioCfg.BaseServerConfig.Timeout),
				Version:        stdioCfg.BaseServerConfig.Version,
				LogJSONRPC:     stdioCfg.BaseServerConfig.LogJSONRPC,
			}
		}
	case mcpmgr.TransportHTTP:
		if httpCfg, ok := mcpmgr.AsHTTP(cfg); ok && httpCfg != nil {
			serialized := &SerializedServerConfig{
				Type:           string(mcpmgr.TransportHTTP),
				Endpoint:       httpCfg.Endpoint,
				SessionID:      httpCfg.SessionID,
				PreferSSE:      boolPtr(httpCfg.PreferSSE),
				TimeoutSeconds: durationToSeconds(httpCfg.BaseServerConfig.Timeout),
				Version:        httpCfg.BaseServerConfig.Version,
				LogJSONRPC:     httpCfg.BaseServerConfig.LogJSONRPC,
			}
			if httpCfg.MaxRetries != 0 {
				serialized.MaxRetries = intPtr(httpCfg.MaxRetries)
			}
			return serialized
		}
	default:
		// Preserve the transport identifier for unknown implementations.
		transport := mcpmgr.TransportOf(cfg)
		if transport == "" {
			transport = "unknown"
		}
		return &SerializedServerConfig{
			Type:           string(transport),
			TimeoutSeconds: 0,
		}
	}
	return nil
}

func durationToSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(d / time.Second)
}

func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func boolPtr(src *bool) *bool {
	if src == nil {
		return nil
	}
	val := *src
	return &val
}

func intPtr(v int) *int {
	return &v
}
