// Package mcpgateway exposes an HTTP-facing aggregation layer that mirrors the
// tools, prompts, and resources managed by mcpmgr over a single Streamable MCP
// server. Downstream clients can connect to one host and transparently proxy
// requests to any configured upstream MCP server without re-implementing
// synchronization, namespace coordination, or elicitation fan-out.
package mcpgateway
