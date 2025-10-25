package mcpgateway

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

func TestGatewayAggregatesRealServers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gateway integration test in short mode")
	}

	const (
		stdioID  = "stdio-example"
		streamID = "streamable-example"
	)

	manager := mcpmgr.NewManager(map[string]mcpmgr.ServerConfig{
		stdioID: &mcpmgr.StdioServerConfig{
			BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 60 * time.Second},
			Command:          "npx",
			Args:             []string{"@modelcontextprotocol/server-everything"},
		},
		streamID: &mcpmgr.HTTPServerConfig{
			BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 60 * time.Second},
			Endpoint:         "https://gitmcp.io/modelcontextprotocol/go-sdk",
		},
	}, &mcpmgr.ManagerOptions{DefaultTimeout: 60 * time.Second})

	t.Cleanup(func() {
		_ = manager.DisconnectAllServers(context.Background())
	})

	gateway, err := NewGateway(manager, &Options{AutoConnect: true, Path: "/mcp"})
	if err != nil {
		t.Fatalf("NewGateway error: %v", err)
	}

	server := httptest.NewServer(gateway.Handler())
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	transport := &mcp.StreamableClientTransport{
		Endpoint:   server.URL + "/mcp",
		HTTPClient: server.Client(),
		MaxRetries: 3,
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-integration-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect to gateway: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	waitCtx, waitCancel := context.WithTimeout(ctx, 60*time.Second)
	defer waitCancel()
	if err := waitForToolPrefixes(waitCtx, session, stdioID+"__", streamID+"__"); err != nil {
		t.Fatalf("gateway did not expose both upstream tool sets: %v", err)
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools via gateway: %v", err)
	}
	if !containsServerMeta(tools.Tools, stdioID) || !containsServerMeta(tools.Tools, streamID) {
		t.Fatalf("gateway tool metadata missing origin ids")
	}
}

func TestGatewayCallAddTool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gateway integration test in short mode")
	}

	const serverID = "stdio-example"
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	manager := mcpmgr.NewManager(nil, &mcpmgr.ManagerOptions{DefaultTimeout: 60 * time.Second})

	cfg := &mcpmgr.StdioServerConfig{
		BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 60 * time.Second},
		Command:          "npx",
		Args:             []string{"@modelcontextprotocol/server-everything"},
	}
	if _, err := manager.ConnectToServer(ctx, serverID, cfg); err != nil {
		t.Fatalf("ConnectToServer(%s): %v", serverID, err)
	}
	t.Cleanup(func() {
		_ = manager.DisconnectAllServers(context.Background())
	})

	gateway, err := NewGateway(manager, &Options{Path: "/mcp"})
	if err != nil {
		t.Fatalf("NewGateway: %v", err)
	}

	server := httptest.NewServer(gateway.Handler())
	t.Cleanup(server.Close)

	transport := &mcp.StreamableClientTransport{
		Endpoint:   server.URL + "/mcp",
		HTTPClient: server.Client(),
		MaxRetries: 3,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-integration-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect to gateway: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	if err := waitForToolPrefixes(ctx, session, serverID+"__"); err != nil {
		t.Fatalf("wait for tool prefix: %v", err)
	}

	toolName := serverID + "__add"
	if err := ensureToolExists(ctx, session, toolName); err != nil {
		t.Fatalf("tool %s not found: %v", toolName, err)
	}

	callCtx, callCancel := context.WithTimeout(ctx, 30*time.Second)
	defer callCancel()
	result, err := session.CallTool(callCtx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: map[string]any{"a": 4, "b": 6},
	})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", toolName, err)
	}
	if result == nil {
		t.Fatalf("CallTool(%s) returned nil result", toolName)
	}
	if result.IsError {
		t.Fatalf("CallTool(%s) reported tool error: %+v", toolName, result.Content)
	}
	if len(result.Content) == 0 && result.StructuredContent == nil {
		t.Fatalf("CallTool(%s) returned empty content", toolName)
	}
}

func waitForToolPrefixes(ctx context.Context, session *mcp.ClientSession, prefixes ...string) error {
	pending := make(map[string]bool, len(prefixes))
	for _, prefix := range prefixes {
		pending[prefix] = false
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		tools, err := session.ListTools(callCtx, nil)
		cancel()
		if err == nil {
			for _, tool := range tools.Tools {
				for prefix := range pending {
					if strings.HasPrefix(tool.Name, prefix) {
						pending[prefix] = true
					}
				}
			}
			allFound := true
			for _, seen := range pending {
				if !seen {
					allFound = false
					break
				}
			}
			if allFound {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func containsServerMeta(tools []*mcp.Tool, serverID string) bool {
	for _, tool := range tools {
		if tool.Meta != nil && tool.Meta[metaKeyServerID] == serverID {
			return true
		}
	}
	return false
}

func ensureToolExists(ctx context.Context, session *mcp.ClientSession, toolName string) error {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tools, err := session.ListTools(listCtx, nil)
	if err != nil {
		return err
	}
	for _, tool := range tools.Tools {
		if tool.Name == toolName {
			return nil
		}
	}
	return fmt.Errorf("tool %s not advertised", toolName)
}
