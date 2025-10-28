package mcpgateway

import (
    "io"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

// Verifies that consumers can add custom routes via ServeMux before serving.
func TestGatewayServeMux_AllowsCustomRoutes_BeforeServe(t *testing.T) {
    manager := mcpmgr.NewManager(nil, nil)
    gateway, err := NewGateway(manager, &Options{Path: "/mcp"})
    if err != nil {
        t.Fatalf("NewGateway: %v", err)
    }

    mux := gateway.ServeMux()
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
        _, _ = w.Write([]byte("ok"))
    })

    srv := httptest.NewServer(gateway.Handler())
    defer srv.Close()

    res, err := http.Get(srv.URL + "/healthz")
    if err != nil {
        t.Fatalf("GET /healthz: %v", err)
    }
    defer res.Body.Close()
    if res.StatusCode != 200 {
        t.Fatalf("GET /healthz status = %d, want 200", res.StatusCode)
    }
    body, _ := io.ReadAll(res.Body)
    if string(body) != "ok" {
        t.Fatalf("GET /healthz body = %q, want \"ok\"", string(body))
    }
}

// Verifies that routes registered after the handler is already mounted are
// reachable. The standard net/http ServeMux permits concurrent registration.
func TestGatewayServeMux_AllowsCustomRoutes_AfterServe(t *testing.T) {
    manager := mcpmgr.NewManager(nil, nil)
    gateway, err := NewGateway(manager, &Options{Path: "/mcp"})
    if err != nil {
        t.Fatalf("NewGateway: %v", err)
    }

    srv := httptest.NewServer(gateway.Handler())
    defer srv.Close()

    // Register a route after the server has started.
    mux := gateway.ServeMux()
    mux.HandleFunc("/late", func(w http.ResponseWriter, r *http.Request) {
        _, _ = w.Write([]byte("ready"))
    })

    res, err := http.Get(srv.URL + "/late")
    if err != nil {
        t.Fatalf("GET /late: %v", err)
    }
    defer res.Body.Close()
    if res.StatusCode != 200 {
        t.Fatalf("GET /late status = %d, want 200", res.StatusCode)
    }
    body, _ := io.ReadAll(res.Body)
    if string(body) != "ready" {
        t.Fatalf("GET /late body = %q, want \"ready\"", string(body))
    }
}

