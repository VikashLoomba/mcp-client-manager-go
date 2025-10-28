// Package mcpgateway exposes an HTTP-facing aggregation layer that mirrors the
// tools, prompts, and resources managed by mcpmgr over a single Streamable MCP
// server. Downstream clients can connect to one host and transparently proxy
// requests to any configured upstream MCP server without re-implementing
// synchronization, namespace coordination, or elicitation fan-out.
//
// The gateway exposes its HTTP surface so applications can extend it:
//   - Use (*Gateway).Handler() to mount the Streamable endpoint into your
//     own HTTP server or mux.
//   - Use (*Gateway).ServeMux() to obtain the internal *http.ServeMux and
//     register additional routes before calling ListenAndServe.
//   - Use (*Gateway).ListenAndServeServer to run a custom *http.Server with
//     your preferred TLS or timeout settings.
package mcpgateway
