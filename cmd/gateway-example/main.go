package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
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

	gwOptions := gateway.Options()
	log.Printf("gateway serving Streamable MCP on %s%s", gwOptions.Addr, gwOptions.Path)
	if err := gateway.ListenAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("gateway server stopped: %v", err)
	}
}
