package mcpgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

func TestGatewayHandlerConditionalBearerToken(t *testing.T) {
	t.Parallel()

	manager := mcpmgr.NewManager(nil, nil)
	const resourceMetadataURL = "https://example-server.modelcontextprotocol.io/.well-known/oauth-protected-resource"

	var verifierCalls int
	gateway, err := NewGateway(manager, &Options{
		Path: "/mcp",
		TokenVerifier: func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
			if token != "valid" {
				return nil, auth.ErrInvalidToken
			}
			verifierCalls++
			return &auth.TokenInfo{
				Expiration: time.Now().Add(time.Minute),
			}, nil
		},
		TokenOptions: &auth.RequireBearerTokenOptions{
			ResourceMetadataURL: resourceMetadataURL,
		},
	})
	if err != nil {
		t.Fatalf("NewGateway with auth: %v", err)
	}

	server := httptest.NewServer(gateway.Handler())
	t.Cleanup(server.Close)

	endpoint := server.URL + "/mcp"
	client := server.Client()

	resp, err := client.Post(endpoint, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post without token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	wantHeader := "Bearer resource_metadata=" + resourceMetadataURL
	if got := resp.Header.Get("WWW-Authenticate"); got != wantHeader {
		t.Fatalf("unexpected WWW-Authenticate header: got %q want %q", got, wantHeader)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer valid")
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("post with token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("expected request with token to reach handler, got 401")
	}
	if verifierCalls != 1 {
		t.Fatalf("expected verifier to be called once, got %d", verifierCalls)
	}
}

func TestGatewayHandlerWithoutAuthLeavesEndpointOpen(t *testing.T) {
	t.Parallel()

	manager := mcpmgr.NewManager(nil, nil)
	gateway, err := NewGateway(manager, &Options{Path: "/mcp"})
	if err != nil {
		t.Fatalf("NewGateway without auth: %v", err)
	}

	server := httptest.NewServer(gateway.Handler())
	t.Cleanup(server.Close)

	resp, err := server.Client().Post(server.URL+"/mcp", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("post without auth config: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("unexpected unauthorized response without auth configured")
	}
}

func TestGatewayAuthOptionsRequireVerifier(t *testing.T) {
	t.Parallel()

	manager := mcpmgr.NewManager(nil, nil)
	_, err := NewGateway(manager, &Options{
		TokenOptions: &auth.RequireBearerTokenOptions{Scopes: []string{"required"}},
	})
	if err == nil {
		t.Fatalf("expected error when TokenOptions provided without TokenVerifier")
	}
}

func TestOAuthProtectedResourceCORSEnabled(t *testing.T) {
	t.Parallel()

	manager := mcpmgr.NewManager(nil, nil)
	gateway, err := NewGateway(manager, &Options{
		TokenVerifier: func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
			return &auth.TokenInfo{
				Expiration: time.Now().Add(time.Minute),
			}, nil
		},
		TokenOptions: &auth.RequireBearerTokenOptions{
			ResourceMetadataURL: "https://example-server.modelcontextprotocol.io/.well-known/oauth-protected-resource",
		},
		AuthorizationServer: "https://example-server.modelcontextprotocol.io/",
	})
	if err != nil {
		t.Fatalf("NewGateway with auth: %v", err)
	}

	server := httptest.NewServer(gateway.Handler())
	t.Cleanup(server.Close)

	metadataEndpoint := server.URL + "/.well-known/oauth-protected-resource"

	t.Run("GET", func(t *testing.T) {
		resp, err := server.Client().Get(metadataEndpoint)
		if err != nil {
			t.Fatalf("get metadata endpoint: %v", err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("unexpected Access-Control-Allow-Origin: got %q", got)
		}
	})

}
