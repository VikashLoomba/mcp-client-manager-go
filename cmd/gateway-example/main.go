package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	mcpgateway "github.com/vikashloomba/mcp-client-manager-go/pkg/mcp-gateway"
	"github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	manager := mcpmgr.NewManager(map[string]mcpmgr.ServerConfig{
		"stdio-example": &mcpmgr.StdioServerConfig{
			BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 15 * time.Second},
			Command:          "npx",
			Args:             []string{"@modelcontextprotocol/server-everything"},
		},
	}, &mcpmgr.ManagerOptions{DefaultClientName: "gateway-example"})

	gateway, err := mcpgateway.NewGateway(manager, &mcpgateway.Options{
		Addr:        ":8787",
		Path:        "/mcp",
		AutoConnect: true,
	})
	if err != nil {
		log.Fatalf("failed to build gateway: %v", err)
	}

	log.Printf("gateway serving Streamable MCP on %s%s", ":8787", "/mcp")
	if err := gateway.ListenAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("gateway stopped: %v", err)
	}
}
