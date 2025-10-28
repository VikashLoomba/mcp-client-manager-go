package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpgateway "github.com/vikashloomba/mcp-client-manager-go/pkg/mcp-gateway"
	"github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

func main() {
	authorizationUrl := os.Getenv("AUTHORIZATION_SERVER_URL")
	oauthResourceMetadataUrl := os.Getenv("OAUTH_RESOURCE_METADATA_URL")
	if authorizationUrl == "" || oauthResourceMetadataUrl == "" {
		authorizationUrl = "https://example-server.modelcontextprotocol.io/"
		oauthResourceMetadataUrl = "https://example-server.modelcontextprotocol.io/.well-known/oauth-protected-resource"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stdioConfig := &mcpmgr.StdioServerConfig{
		BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 15 * time.Second},
		Command:          "npx",
		Args:             []string{"@modelcontextprotocol/server-everything"},
	}

	manager := mcpmgr.NewManager(nil, &mcpmgr.ManagerOptions{DefaultClientName: "gateway-example"})

	verifier := func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		// Validate token with your upstream authorization server
		// Return TokenInfo with scopes, expiration, etc.
		return &auth.TokenInfo{
			// Scopes:     []string{""},
			Expiration: time.Now().Add(time.Hour),
		}, nil
	}

	gatewayOpts := &mcpgateway.Options{
		Addr:        ":8787",
		Path:        "/mcp",
		AutoConnect: true,
		Streamable: mcp.StreamableHTTPOptions{
			Stateless:    false,
			JSONResponse: true,
		},
	}
	if authorizationUrl != "" && oauthResourceMetadataUrl != "" {
		gatewayOpts.TokenVerifier = verifier
		gatewayOpts.TokenOptions = &auth.RequireBearerTokenOptions{
			ResourceMetadataURL: oauthResourceMetadataUrl,
		}
		gatewayOpts.AuthorizationServer = authorizationUrl
	}

	gateway, err := mcpgateway.NewGateway(manager, gatewayOpts)
	if err != nil {
		log.Fatalf("failed to build gateway: %v", err)
	}

	if err := gateway.AttachServer(ctx, "stdio-example", stdioConfig); err != nil {
		log.Fatalf("failed to attach stdio-example server: %v", err)
	}

	// Expose simple management routes on the same HTTP server.
	mux := gateway.ServeMux()
	mux.HandleFunc("/servers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			// List configured/known servers with basic status.
			list := manager.GetServerSummaries()
			_ = json.NewEncoder(w).Encode(list)
		case http.MethodPost:
			// Add and attach a server. Accepts either stdio or http types.
			var req struct {
				ID             string            `json:"id"`
				Type           string            `json:"type"` // "stdio" or "http"
				Command        string            `json:"command"`
				Args           []string          `json:"args"`
				Env            map[string]string `json:"env"`
				Endpoint       string            `json:"endpoint"`
				PreferSSE      *bool             `json:"preferSSE"`
				TimeoutSeconds int               `json:"timeoutSeconds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid JSON"})
				return
			}
			if req.ID == "" || req.Type == "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "id and type are required"})
				return
			}
			var cfg mcpmgr.ServerConfig
			timeout := time.Duration(req.TimeoutSeconds) * time.Second
			switch req.Type {
			case "stdio":
				if req.Command == "" {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": "command required for stdio type"})
					return
				}
				cfg = &mcpmgr.StdioServerConfig{
					BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: timeout},
					Command:          req.Command,
					Args:             append([]string(nil), req.Args...),
					Env:              req.Env,
				}
			case "http":
				if req.Endpoint == "" {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": "endpoint required for http type"})
					return
				}
				cfg = &mcpmgr.HTTPServerConfig{
					BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: timeout},
					Endpoint:         req.Endpoint,
					PreferSSE:        req.PreferSSE,
				}
			default:
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "type must be 'stdio' or 'http'"})
				return
			}
			if err := gateway.AttachServer(r.Context(), req.ID, cfg); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "attached", "id": req.ID})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/servers/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := strings.TrimPrefix(r.URL.Path, "/servers/")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "missing server id in path"})
			return
		}
		switch r.Method {
		case http.MethodDelete:
			if err := gateway.RemoveServer(r.Context(), id); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "removed", "id": id})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	gwOptions := gateway.Options()
	log.Printf("gateway serving Streamable MCP on %s%s", gwOptions.Addr, gwOptions.Path)
	if err := gateway.ListenAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("gateway server stopped: %v", err)
	}
}
