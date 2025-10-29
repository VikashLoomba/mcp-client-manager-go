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

func (s *McpService) GetServersWithDetails()  {

}
