package main

import (
	"context"
	"fmt"
	"time"

	"github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

func main() {
	manager := mcpmgr.NewManager(map[string]mcpmgr.ServerConfig{
		"example-stdio": &mcpmgr.StdioServerConfig{
			BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 10 * time.Second},
			Command:          "./my-mcp-server",
			Args:             []string{"--serve"},
		},
	}, &mcpmgr.ManagerOptions{DefaultClientName: "manager-example"})

	ctx := context.Background()
	servers := manager.ListServers()
	summaries := manager.GetServerSummaries()
	for _, id := range servers {
		fmt.Printf("Configured server: %s\n", id)
		for _, summary := range summaries {
			if summary.ID == id {
				fmt.Printf("Status: %s\n", summary.Status)
				break
			}
		}
	}

	if err := manager.DisconnectAllServers(ctx); err != nil {
		fmt.Printf("disconnect error: %v\n", err)
	}
}
