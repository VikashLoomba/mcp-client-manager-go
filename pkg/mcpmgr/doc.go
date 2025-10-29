// Package mcpmgr centralizes the management of multiple Model Context Protocol
// (MCP) transports from a single Go process. It layers connection lifecycle
// tracking, reconnection, and notification fan-out on top of the
// modelcontextprotocol/go-sdk client so callers can focus on consuming tools,
// prompts, and resources instead of rebuilding MPC plumbing.
//
// # Core entry points
//
//   - Manager is the long-lived orchestration type. Construct it with
//     NewManager, then call ConnectToServer / DisconnectServer or use
//     AutoConnect to dial transports automatically.
//   - ServerConfig (and the HTTPServerConfig / StdioServerConfig variants)
//     declare how each MCP server should be launched or contacted.
//   - ManagerOptions toggle default client identifiers, timeouts, JSON-RPC
//     logging, notification buffer sizes, and whether to dial servers eagerly.
//
// After a server is connected, use helpers such as ListTools, ExecuteTool,
// ListPrompts, ListResources, and ReadResource to interrogate its declared
// capabilities. Event-driven integrations can register notification handlers
// via OnToolListChanged, OnResourceUpdated, or AddNotificationHandler. To
// support interactive prompts, SetElicitationCallback exposes pending
// ElicitationEvent objects and RespondToElicitation completes them once the UI
// has collected user input.
//
// See README.md for end-to-end setup examples and recommended initialization
// patterns. When inspecting server configurations returned from
// GetServerSummaries or GetServerConfig, use the helper guards and narrowers
// (IsStdio/IsHTTP and AsStdio/AsHTTP) or TransportOf to branch on the concrete
// transport type and build JSON‑safe views when serializing (avoid marshaling
// BaseServerConfig directly because it contains function fields).
package mcpmgr
