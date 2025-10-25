# MCP Client Manager (Go)

`mcpmgr` is a lightweight orchestration layer around the
[modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk)
client. It keeps multiple MCP transports (stdio or HTTP) alive, fans out
events, and exposes ergonomic helpers for listing or invoking tools, prompts,
and resources from Go applications. The companion `mcpgateway` package builds
on top of `mcpmgr` to expose every managed server through a single Streamable
HTTP endpoint.

## Install

```bash
go get github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr
go get github.com/vikashloomba/mcp-client-manager-go/pkg/mcp-gateway
```

## Initialize with pre-registered servers

```go
package main

import (
    "context"
    "time"

    "github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

func main() {
    manager := mcpmgr.NewManager(map[string]mcpmgr.ServerConfig{
        "stdio-example": &mcpmgr.StdioServerConfig{
            BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 30 * time.Second},
            Command:          "npx",
            Args:             []string{"@modelcontextprotocol/server-everything"},
        },
        "streamable-example": &mcpmgr.HTTPServerConfig{
            BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 30 * time.Second},
            Endpoint:         "https://gitmcp.io/modelcontextprotocol/go-sdk",
        },
    }, &mcpmgr.ManagerOptions{DefaultClientName: "my-app", AutoConnect: true})

    ctx := context.Background()
    // AutoConnect will dial the transports in the background; ensure you close
    // them before exiting.
    defer manager.DisconnectAllServers(ctx)
}
```

## Add a server after initialization

```go
ctx := context.Background()

config := &mcpmgr.HTTPServerConfig{
    BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 45 * time.Second},
    Endpoint:         "https://gitmcp.io/modelcontextprotocol/go-sdk",
}

if _, err := manager.ConnectToServer(ctx, "docs-server", config); err != nil {
    panic(err)
}
```

## List and call tools

```go
tools, err := manager.ListTools(ctx, "streamable-example", nil)
if err != nil {
    panic(err)
}

for _, tool := range tools.Tools {
    println("Tool:", tool.Name)
}

result, err := manager.ExecuteTool(ctx, "streamable-example", "fetch_url_content", map[string]any{
    "url": "https://example.com",
})
if err != nil {
    panic(err)
}
println("Result:", result.Content)
```

## Read prompts and resources

```go
prompts, err := manager.ListPrompts(ctx, "stdio-example", nil)
if err != nil {
    panic(err)
}
for _, prompt := range prompts.Prompts {
    println("Prompt:", prompt.Name)
}

resources, err := manager.ListResources(ctx, "stdio-example", nil)
if err != nil {
    panic(err)
}
for _, resource := range resources.Resources {
    println("Resource:", resource.URI)
}

details, err := manager.ReadResource(ctx, "stdio-example", &mcp.ReadResourceParams{URI: resources.Resources[0].URI})
if err != nil {
    panic(err)
}
println("First resource size:", len(details.Resource.Data))
```

## Run a Streamable MCP gateway

The `mcpgateway` package re-exports every tool, prompt, and resource managed by
`mcpmgr` through a single Streamable HTTP endpoint so downstream clients only
have to connect once.

```go
package main

import (
    "context"
    "log"
    "time"

    mcpgateway "github.com/vikashloomba/mcp-client-manager-go/pkg/mcp-gateway"
    "github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

func main() {
    manager := mcpmgr.NewManager(map[string]mcpmgr.ServerConfig{
        "stdio-example": &mcpmgr.StdioServerConfig{
            BaseServerConfig: mcpmgr.BaseServerConfig{Timeout: 30 * time.Second},
            Command:          "npx",
            Args:             []string{"@modelcontextprotocol/server-everything"},
        },
    }, &mcpmgr.ManagerOptions{DefaultClientName: "gateway-example", AutoConnect: true})

    gateway, err := mcpgateway.NewGateway(manager, &mcpgateway.Options{Addr: ":8787", Path: "/mcp"})
    if err != nil {
        log.Fatalf("gateway init failed: %v", err)
    }

    ctx := context.Background()
    defer manager.DisconnectAllServers(ctx)

    log.Println("Serving MCP gateway on http://localhost:8787/mcp")
    if err := gateway.ListenAndServe(ctx); err != nil {
        log.Fatalf("gateway stopped: %v", err)
    }
}
```

Check `cmd/gateway-example` for a runnable sample and the package docs under
`pkg/mcp-gateway` for customization options like namespace strategies,
notification hooks, and elicitation bridging.

### Optional OAuth 2.0 protection

`mcpgateway` can require bearer tokens before it forwards any MCP traffic. To
enable this, populate `mcpgateway.Options.TokenVerifier` with your token
inspection logic and provide matching `TokenOptions`
(`auth.RequireBearerTokenOptions`). When both are present, the gateway wraps the
Streamable handler with `auth.RequireBearerToken`, automatically hosts
`/.well-known/oauth-protected-resource`, replies with the correct
`WWW-Authenticate: Bearer resource_metadata=…` header, and enables CORS on the
metadata endpoint. Set `Options.AuthorizationServer` so the metadata response
links to your issuer, and override `Options.ResourceURL` if the public URL
clients will use does not match `http://localhost<Addr><Path>`.

The sample in `cmd/gateway-example` toggles this behavior with environment
variables so you can opt in without changing code:

| Variable | Description | Example value |
| --- | --- | --- |
| `AUTHORIZATION_SERVER_URL` | Base URL of the OAuth 2.0/OIDC authorization server that issues gateway tokens. | `https://example-server.modelcontextprotocol.io/` |
| `OAUTH_RESOURCE_METADATA_URL` | Fully-qualified URL where clients discover the gateway's resource metadata. This should usually be your deployed gateway's `/.well-known/oauth-protected-resource`. | `https://example-server.modelcontextprotocol.io/.well-known/oauth-protected-resource` |

`mcpgateway.Options.ResourceURL` controls the `"resource"` value returned from
the metadata endpoint. It defaults to `http://localhost<Addr><Path>`, matching
the in-process listener, so set it explicitly when your public gateway URL lives
behind a proxy or uses TLS/hostnames that differ from the local listener.

If both variables are set, the example attaches your verifier and protects the
Streamable endpoint; if either is empty, the gateway remains open for local
development. A minimal launch sequence looks like:

```bash
export AUTHORIZATION_SERVER_URL="https://example-server.modelcontextprotocol.io/"
export OAUTH_RESOURCE_METADATA_URL="https://example-server.modelcontextprotocol.io/.well-known/oauth-protected-resource"
go run ./cmd/gateway-example
```

Inside the sample `TokenVerifier`, replace the placeholder logic with real JWT
inspection or token introspection calls against your authorization server.

## Respond to elicitation requests

```go
manager.SetElicitationCallback(func(ctx context.Context, event *mcpmgr.ElicitationEvent) (*mcp.ElicitResult, error) {
    println("Elicitation from", event.ServerID, "message:", event.Message)

    // Option A: respond immediately.
    return &mcp.ElicitResult{Content: []any{"acknowledged"}}, nil
})

// Option B: defer the response and use RespondToElicitation later.

manager.SetElicitationCallback(func(ctx context.Context, event *mcpmgr.ElicitationEvent) (*mcp.ElicitResult, error) {
    go func(id string) {
        manager.RespondToElicitation(id, &mcp.ElicitResult{Content: []any{"done"}})
    }(event.RequestID)
    return nil, nil // pending response will be delivered asynchronously.
})
```

## Next steps

- Register notification handlers via `OnToolListChanged`, `OnResourceUpdated`,
  or `AddNotificationHandler` to react to server events.
- Provide a custom `RPCLogger` through `ManagerOptions` to emit JSON-RPC traffic
  to your logging or observability pipeline.

## Releases

Pushes to the `main` branch automatically cut a release via
`.github/workflows/release.yml`:

- The workflow compares `HEAD` to the most recent `v*.*.*` tag and bumps the
  version (patch by default). Include `#minor` or `#major` in a commit message
  to request larger bumps; `BREAKING CHANGE` also forces a major release.
- After tests succeed, cross-platform `manager-example` binaries are built and
  uploaded as release assets. The workflow then creates the tag, publishes the
  GitHub release with a changelog, and triggers pkg.go.dev.
