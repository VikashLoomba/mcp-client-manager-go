# gateway-example

Minimal example of running the MCP Gateway in-process with:
- The Streamable MCP endpoint mounted at `/mcp`.
- Simple management routes to add, remove, and list servers.
- Optional bearer‑token protection for the MCP endpoint (opt‑in via env vars).

## Prerequisites
- Go 1.22+
- For the default stdio example server, Node.js with `npx` available (used to run `@modelcontextprotocol/server-everything`).

## Run
```bash
# From repo root
go run ./cmd/gateway-example
```

By default it starts on `http://localhost:8787` and:
- Exposes the MCP endpoint at `http://localhost:8787/mcp`
- Registers example management routes under the same server

Press Ctrl+C to exit.

## Optional: enable bearer‑token auth for MCP endpoint
Set both env vars to enable auth for `/mcp` and expose resource metadata under `/.well-known/oauth-protected-resource`.

```bash
export AUTHORIZATION_SERVER_URL="https://example-server.modelcontextprotocol.io/"
export OAUTH_RESOURCE_METADATA_URL="https://example-server.modelcontextprotocol.io/.well-known/oauth-protected-resource"

go run ./cmd/gateway-example
```

Notes:
- Only the MCP endpoint is wrapped by the bearer token middleware. The management routes below remain open; protect them separately if needed.
- `OAUTH_RESOURCE_METADATA_URL` should point to the deployed gateway’s well‑known URL in production.

## Management routes
These are implemented in `cmd/gateway-example/main.go` using `gateway.ServeMux()`.

- GET `/servers` — list configured/known servers with connection status
  ```bash
  curl http://localhost:8787/servers | jq
  ```

- POST `/servers` — attach a server configuration at runtime
  - stdio server
    ```bash
    curl -sS -X POST http://localhost:8787/servers \
      -H 'Content-Type: application/json' \
      -d '{
            "id": "stdio-1",
            "type": "stdio",
            "command": "npx",
            "args": ["@modelcontextprotocol/server-everything"],
            "timeoutSeconds": 15
          }'
    ```
  - http server
    ```bash
    curl -sS -X POST http://localhost:8787/servers \
      -H 'Content-Type: application/json' \
      -d '{
            "id": "http-1",
            "type": "http",
            "endpoint": "http://localhost:8700/mcp",
            "preferSSE": true,
            "timeoutSeconds": 15
          }'
    ```

- DELETE `/servers/{id}` — detach and remove a server
  ```bash
  curl -X DELETE http://localhost:8787/servers/http-1
  ```

## Extending
You can register additional routes on the same server using the exposed mux:
```go
mux := gateway.ServeMux()
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
```

If you prefer to bring your own `*http.Server` with TLS or custom timeouts, use:
```go
srv := &http.Server{Addr: ":8787"}
_ = gateway.ListenAndServeServer(ctx, srv)
```

## Security
The management routes in this example are unauthenticated and intended for local development. If you deploy this example, add authentication/authorization and appropriate network controls before exposing these endpoints.
