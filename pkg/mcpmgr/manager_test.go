package mcpmgr

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestManagerInitialServersAndSummaries(t *testing.T) {
	t.Parallel()

	stdioID := "stdio-example"
	streamID := "streamable-example"

	cfg := map[string]ServerConfig{
		stdioID: &StdioServerConfig{
			BaseServerConfig: BaseServerConfig{Timeout: 5 * time.Second},
			Command:          "npx",
			Args:             []string{"@modelcontextprotocol/server-everything"},
		},
		streamID: &HTTPServerConfig{
			BaseServerConfig: BaseServerConfig{Timeout: 5 * time.Second},
			Endpoint:         "https://gitmcp.io/modelcontextprotocol/go-sdk",
		},
	}

	manager := NewManager(cfg, &ManagerOptions{DefaultClientName: "manager-tests"})

	servers := manager.ListServers()
	expectedIDs := []string{stdioID, streamID}
	if !reflect.DeepEqual(servers, expectedIDs) {
		t.Fatalf("ListServers() = %v, expected %v", servers, expectedIDs)
	}

	if !manager.HasServer(stdioID) || !manager.HasServer(streamID) {
		t.Fatalf("manager should know both configured servers")
	}

	stdioCfg, ok := manager.GetServerConfig(stdioID).(*StdioServerConfig)
	if !ok {
		t.Fatalf("expected stdio config for %s", stdioID)
	}
	if stdioCfg.Command != "npx" || len(stdioCfg.Args) != 1 || stdioCfg.Args[0] != "@modelcontextprotocol/server-everything" {
		t.Fatalf("stdio config not preserved: %#v", stdioCfg)
	}

	httpCfg, ok := manager.GetServerConfig(streamID).(*HTTPServerConfig)
	if !ok {
		t.Fatalf("expected http config for %s", streamID)
	}
	if httpCfg.Endpoint != "https://gitmcp.io/modelcontextprotocol/go-sdk" {
		t.Fatalf("http endpoint mismatch: %s", httpCfg.Endpoint)
	}

	summaries := manager.GetServerSummaries()
	if len(summaries) != 2 {
		t.Fatalf("expected two summaries, got %d", len(summaries))
	}
	for _, summary := range summaries {
		if summary.Status != StatusDisconnected {
			t.Fatalf("expected disconnected status for %s, got %s", summary.ID, summary.Status)
		}
		switch c := summary.Config.(type) {
		case *StdioServerConfig:
			if summary.ID != stdioID {
				t.Fatalf("stdio summary attached to %s", summary.ID)
			}
			if c.Command != "npx" || c.Args[0] != "@modelcontextprotocol/server-everything" {
				t.Fatalf("summary stdio config mismatch: %#v", c)
			}
		case *HTTPServerConfig:
			if summary.ID != streamID {
				t.Fatalf("http summary attached to %s", summary.ID)
			}
			if c.Endpoint != "https://gitmcp.io/modelcontextprotocol/go-sdk" {
				t.Fatalf("summary http endpoint mismatch: %s", c.Endpoint)
			}
		default:
			t.Fatalf("unexpected config type: %T", summary.Config)
		}
	}
}

func TestHTTPServerFetchURLContentToolExists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	streamID := "streamable-example"
	manager := NewManager(map[string]ServerConfig{
		streamID: &HTTPServerConfig{
			BaseServerConfig: BaseServerConfig{Timeout: 60 * time.Second},
			Endpoint:         "https://gitmcp.io/modelcontextprotocol/go-sdk",
		},
	}, &ManagerOptions{DefaultTimeout: 60 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	defer manager.DisconnectAllServers(context.Background())

	if _, err := manager.ConnectToServer(ctx, streamID, nil); err != nil {
		t.Fatalf("ConnectToServer(%s): %v", streamID, err)
	}

	tools, err := manager.ListTools(ctx, streamID, nil)
	if err != nil {
		t.Fatalf("ListTools(%s): %v", streamID, err)
	}

	for _, tool := range tools.Tools {
		t.Logf("Tool: %s", tool.Name)
		if tool.Name == "fetch_generic_url_content" {
			return
		}
	}
	t.Fatalf("fetch_url_content tool not found on %s", streamID)
}

func TestManagerBuildStdioTransportForServerEverything(t *testing.T) {
	t.Parallel()

	manager := NewManager(nil, nil)
	cfg := &StdioServerConfig{
		BaseServerConfig: BaseServerConfig{Timeout: 5 * time.Second},
		Command:          "npx",
		Args:             []string{"@modelcontextprotocol/server-everything"},
		Env:              map[string]string{"MCP_SERVER_MODE": "stdio"},
	}

	transport, err := manager.buildStdioTransport("stdio-example", cfg)
	if err != nil {
		t.Fatalf("buildStdioTransport error: %v", err)
	}

	cmdTransport, ok := transport.(*mcp.CommandTransport)
	if !ok {
		t.Fatalf("expected CommandTransport, got %T", transport)
	}

	expectedArgs := append([]string{cfg.Command}, cfg.Args...)
	if !reflect.DeepEqual(cmdTransport.Command.Args, expectedArgs) {
		t.Fatalf("command args = %v, expected %v", cmdTransport.Command.Args, expectedArgs)
	}

	if !envContains(cmdTransport.Command.Env, "MCP_SERVER_MODE", "stdio") {
		t.Fatalf("env missing MCP_SERVER_MODE from stdio config")
	}
}

func TestStdioServerEverythingProvidesPromptsToolsResources(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	serverID := "stdio-example"
	manager := NewManager(map[string]ServerConfig{
		serverID: &StdioServerConfig{
			BaseServerConfig: BaseServerConfig{Timeout: 60 * time.Second},
			Command:          "npx",
			Args:             []string{"@modelcontextprotocol/server-everything"},
		},
	}, &ManagerOptions{DefaultTimeout: 60 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer manager.DisconnectAllServers(context.Background())

	if _, err := manager.ConnectToServer(ctx, serverID, nil); err != nil {
		t.Fatalf("ConnectToServer(%s): %v", serverID, err)
	}

	tools, err := manager.ListTools(ctx, serverID, nil)
	if err != nil {
		t.Fatalf("ListTools(%s): %v", serverID, err)
	}
	if len(tools.Tools) == 0 {
		t.Fatalf("expected at least one tool from %s", serverID)
	}

	prompts, err := manager.ListPrompts(ctx, serverID, nil)
	if err != nil {
		t.Fatalf("ListPrompts(%s): %v", serverID, err)
	}
	if len(prompts.Prompts) == 0 {
		t.Fatalf("expected at least one prompt from %s", serverID)
	}

	resources, err := manager.ListResources(ctx, serverID, nil)
	if err != nil {
		t.Fatalf("ListResources(%s): %v", serverID, err)
	}
	if len(resources.Resources) == 0 {
		t.Fatalf("expected at least one resource from %s", serverID)
	}
}

func TestManagerDecorateHTTPClientAddsHeadersAndSession(t *testing.T) {
	t.Parallel()

	manager := NewManager(nil, nil)
	tracker := newSessionIDTracker("session-stdio-http")
	headers := http.Header{"X-MCP-Source": []string{"manager-tests"}}
	providerCalled := false
	provider := func(ctx context.Context) (string, error) {
		providerCalled = true
		return "Bearer example-token", nil
	}

	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("X-MCP-Source"); got != "manager-tests" {
			t.Fatalf("decorated header missing, got %q", got)
		}
		if got := req.Header.Get(sessionIDHeaderName); got != "session-stdio-http" {
			t.Fatalf("session header missing, got %q", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer example-token" {
			t.Fatalf("auth header mismatch, got %q", got)
		}
		if req.URL.String() != "https://gitmcp.io/modelcontextprotocol/go-sdk" {
			t.Fatalf("request hit unexpected URL: %s", req.URL)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})

	baseClient := &http.Client{Transport: rt}
	decorated := manager.decorateHTTPClient(baseClient, headers, tracker, provider)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://gitmcp.io/modelcontextprotocol/go-sdk", nil)
	if err != nil {
		t.Fatalf("request creation failed: %v", err)
	}
	resp, err := decorated.Do(req)
	if err != nil {
		t.Fatalf("decorated client Do error: %v", err)
	}
	_ = resp.Body.Close()
	if !providerCalled {
		t.Fatalf("auth provider was not invoked")
	}
}

func TestManagerShouldPreferSSEHeuristic(t *testing.T) {
	t.Parallel()

	manager := NewManager(nil, nil)

	httpCfg := &HTTPServerConfig{Endpoint: "https://gitmcp.io/modelcontextprotocol/go-sdk"}
	if manager.shouldPreferSSE(httpCfg) {
		t.Fatalf("did not expect SSE preference for non-sse endpoint")
	}

	sseCfg := &HTTPServerConfig{Endpoint: "https://gitmcp.io/modelcontextprotocol/go-sdk/sse"}
	if !manager.shouldPreferSSE(sseCfg) {
		t.Fatalf("expected SSE preference for /sse endpoint")
	}

	override := true
	overrideCfg := &HTTPServerConfig{Endpoint: "https://gitmcp.io/modelcontextprotocol/go-sdk", PreferSSE: &override}
	if !manager.shouldPreferSSE(overrideCfg) {
		t.Fatalf("explicit PreferSSE=true should win")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func envContains(env []string, key, value string) bool {
	target := key + "=" + value
	for _, item := range env {
		if item == target {
			return true
		}
	}
	return false
}
