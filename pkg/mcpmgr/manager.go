// Package mcpmgr provides a high-level manager for connecting to, monitoring,
// and coordinating multiple Model Context Protocol (MCP) servers from Go
// applications. It handles transport setup (stdio or HTTP), automatic session
// lifecycle management, notification fan-out, and convenience helpers for
// invoking MCP methods such as listing tools, prompts, or resources. Importers
// can use Manager to keep long-lived MCP connections alive, observe
// connectivity, and centralize elicitation handling without re‑implementing
// the MCP client plumbing.
package mcpmgr

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ConnectionStatus represents the lifecycle of a managed connection.
type ConnectionStatus string

const (
	StatusDisconnected ConnectionStatus = "disconnected"
	StatusConnecting   ConnectionStatus = "connecting"
	StatusConnected    ConnectionStatus = "connected"
)

const sessionIDHeaderName = "Mcp-Session-Id"

// ServerSummary aggregates status information for a managed server.
type ServerSummary struct {
	ID     string
	Status ConnectionStatus
	Config ServerConfig
}

// ElicitationHandler mirrors the MCP client elicitation handler signature.
type ElicitationHandler func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error)

// NotificationSchema identifies an MCP notification method.
type NotificationSchema string

const (
	NotificationSchemaToolListChanged     NotificationSchema = "notifications/tools/list_changed"
	NotificationSchemaPromptListChanged   NotificationSchema = "notifications/prompts/list_changed"
	NotificationSchemaResourceListChanged NotificationSchema = "notifications/resources/list_changed"
	NotificationSchemaResourceUpdated     NotificationSchema = "notifications/resources/updated"
	NotificationSchemaLogging             NotificationSchema = "notifications/logging/message"
	NotificationSchemaProgress            NotificationSchema = "notifications/progress"
)

// NotificationPayload carries the raw request associated with a notification so
// callers can perform custom decoding when necessary.
type NotificationPayload struct {
	ServerID string
	Method   NotificationSchema
	Request  mcp.Request
}

// NotificationHandlerFunc mirrors the TypeScript manager's ability to register
// handlers for arbitrary notification schemas.
type NotificationHandlerFunc func(context.Context, NotificationPayload)

// GlobalElicitationCallback is invoked when no server-specific handler is
// registered and provides the request context to the caller.
type GlobalElicitationCallback func(context.Context, *ElicitationEvent) (*mcp.ElicitResult, error)

// ElicitationEvent surfaces the information required to construct a UI for an
// elicitation request.
type ElicitationEvent struct {
	ServerID  string
	RequestID string
	Message   string
	Schema    any
	Params    *mcp.ElicitRequest
	CreatedAt time.Time
}

// PendingElicitationInfo is returned to callers inspecting the pending queue.
type PendingElicitationInfo struct {
	Event ElicitationEvent
}

// Manager orchestrates multiple MCP client sessions.
type Manager struct {
	mu sync.RWMutex

	options ManagerOptions

	states map[string]*managedState

	notifications    map[string]*notificationRegistry
	rawNotifications map[string]map[NotificationSchema][]NotificationHandlerFunc

	serverElicitations    map[string]ElicitationHandler
	globalElicitation     ElicitationHandler
	globalElicitationFunc GlobalElicitationCallback
	pendingElicitations   map[string]*pendingElicitation

	defaultLogJSONRPC bool

	// serverRemovedHandlers are invoked after a server is removed via RemoveServer.
	serverRemovedHandlers []func(string)
}

type managedState struct {
	config ServerConfig

	timeout time.Duration

	client         *mcp.Client
	session        *mcp.ClientSession
	sessionTracker *sessionIDTracker

	connecting bool
	connectCh  chan struct{}

	toolsMeta map[string]map[string]any
}

type notificationRegistry struct {
	toolListHandlers       []func(context.Context, *mcp.ToolListChangedRequest)
	promptListHandlers     []func(context.Context, *mcp.PromptListChangedRequest)
	resourceListHandlers   []func(context.Context, *mcp.ResourceListChangedRequest)
	resourceUpdateHandlers []func(context.Context, *mcp.ResourceUpdatedNotificationRequest)
}

// NewManager constructs a Manager with optional initial server configurations.
// Pass a map of server IDs to configs to pre-register transports and, when
// ManagerOptions.AutoConnect is true, establish sessions automatically.
// Callers can provide nil options to fall back to sensible defaults.
func NewManager(cfg map[string]ServerConfig, opts *ManagerOptions) *Manager {
	options := opts.normalized()
	if options.DefaultClientVersion == "" {
		options.DefaultClientVersion = "1.0.0"
	}
	if options.DefaultTimeout <= 0 {
		options.DefaultTimeout = 30 * time.Second
	}
	m := &Manager{
		options:             options,
		states:              make(map[string]*managedState),
		notifications:       make(map[string]*notificationRegistry),
		rawNotifications:    make(map[string]map[NotificationSchema][]NotificationHandlerFunc),
		serverElicitations:  make(map[string]ElicitationHandler),
		pendingElicitations: make(map[string]*pendingElicitation),
		defaultLogJSONRPC:   options.DefaultLogJSONRPC,
	}
	for id, sc := range cfg {
		m.states[id] = &managedState{
			config:         sc,
			toolsMeta:      make(map[string]map[string]any),
			sessionTracker: newSessionIDTracker(""),
		}
		if options.AutoConnect {
			go func(serverID string) {
				ctx, cancel := context.WithTimeout(context.Background(), m.options.DefaultTimeout)
				defer cancel()
				_, _ = m.ConnectToServer(ctx, serverID, nil)
			}(id)
		}
	}
	return m
}

// ListServers returns known server identifiers.
func (m *Manager) ListServers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.states))
	for id := range m.states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// HasServer reports whether a server ID is known.
func (m *Manager) HasServer(serverID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.states[serverID]
	return ok
}

// GetServerSummaries returns status snapshots for all managed servers,
// including the last-known config and an up-to-date ConnectionStatus derived
// from a lightweight ping.
func (m *Manager) GetServerSummaries() []ServerSummary {
	m.mu.RLock()
	summaries := make([]ServerSummary, 0, len(m.states))
	for id, st := range m.states {
		summaries = append(summaries, ServerSummary{ID: id, Config: st.config})
	}
	m.mu.RUnlock()

	ctx := context.Background()
	for idx := range summaries {
		serverID := summaries[idx].ID
		summaries[idx].Status = m.GetConnectionStatusByAttemptingPing(ctx, serverID)
	}
	return summaries
}

// GetServerConfig returns a shallow copy of the configuration for a given
// server, allowing callers to inspect launch settings before connecting.
func (m *Manager) GetServerConfig(serverID string) ServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if st, ok := m.states[serverID]; ok {
		return st.config
	}
	return nil
}

// GetClient exposes the underlying MCP client for advanced scenarios such as
// manual middleware injection or direct SDK calls. The value can be nil when
// the server has not yet been connected.
func (m *Manager) GetClient(serverID string) *mcp.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if st, ok := m.states[serverID]; ok {
		return st.client
	}
	return nil
}

// ConnectToServer establishes (or reuses) a client session using the provided
// configuration. When cfg is nil, the previously registered configuration is
// used. The returned session stays cached until it closes or DisconnectServer
// is invoked.
func (m *Manager) ConnectToServer(ctx context.Context, serverID string, cfg ServerConfig) (*mcp.ClientSession, error) {
	for {
		m.mu.Lock()
		state, ok := m.states[serverID]
		if !ok {
			if cfg == nil {
				m.mu.Unlock()
				return nil, fmt.Errorf("mcpmgr: unknown server %q", serverID)
			}
			state = &managedState{
				toolsMeta:      make(map[string]map[string]any),
				sessionTracker: newSessionIDTracker(""),
			}
			m.states[serverID] = state
		}
		if cfg != nil {
			state.config = cfg
		}
		if state.config == nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("mcpmgr: missing configuration for %q", serverID)
		}
		if state.session != nil {
			session := state.session
			m.mu.Unlock()
			return session, nil
		}
		if state.connecting {
			ch := state.connectCh
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ch:
				continue
			}
		}
		state.connecting = true
		state.connectCh = make(chan struct{})
		base := state.config.base()
		timeout := base.Timeout
		if timeout <= 0 {
			timeout = m.options.DefaultTimeout
		}
		state.timeout = timeout
		m.mu.Unlock()

		session, err := m.establishSession(ctx, serverID, state)
		m.mu.Lock()
		state.connecting = false
		close(state.connectCh)
		if err != nil {
			if state.session == nil {
				state.client = nil
			}
			m.mu.Unlock()
			return nil, err
		}
		state.session = session
		m.mu.Unlock()
		return session, nil
	}
}

func (m *Manager) establishSession(ctx context.Context, serverID string, state *managedState) (*mcp.ClientSession, error) {
	base := state.config.base()
	impl := &mcp.Implementation{
		Name:    m.effectiveClientName(serverID),
		Version: m.effectiveClientVersion(base),
	}
	clientOpts := m.composeClientOptions(serverID, base)
	logger := m.resolveLogger(serverID, base)

	attempt := func(ctx context.Context, transport mcp.Transport) (*mcp.ClientSession, *mcp.Client, error) {
		client := mcp.NewClient(impl, &clientOpts)
		client.AddReceivingMiddleware(m.notificationMiddleware(serverID))
		wrapped := transport
		if logger != nil {
			wrapped = &loggingTransport{serverID: serverID, delegate: transport, logger: logger}
		}
		session, err := client.Connect(ctx, wrapped, nil)
		if err != nil {
			return nil, nil, err
		}
		return session, client, nil
	}

	connectCtx := ctx
	if state.timeout > 0 {
		var cancel context.CancelFunc
		connectCtx, cancel = context.WithTimeout(ctx, state.timeout)
		defer cancel()
	}

	switch cfg := state.config.(type) {
	case *StdioServerConfig:
		transport, err := m.buildStdioTransport(serverID, cfg)
		if err != nil {
			return nil, err
		}
		session, client, err := attempt(connectCtx, transport)
		if err != nil {
			return nil, err
		}
		state.client = client
		go m.monitorSession(serverID, session, base)
		return session, nil
	case *HTTPServerConfig:
		return m.establishHTTPSession(connectCtx, serverID, state, base, cfg, attempt)
	default:
		return nil, fmt.Errorf("mcpmgr: unsupported config for %q", serverID)
	}
}

func (m *Manager) establishHTTPSession(
	ctx context.Context,
	serverID string,
	state *managedState,
	base *BaseServerConfig,
	cfg *HTTPServerConfig,
	attempt func(context.Context, mcp.Transport) (*mcp.ClientSession, *mcp.Client, error),
) (*mcp.ClientSession, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("mcpmgr: endpoint missing for %q", serverID)
	}
	tracker := state.sessionTracker
	if tracker == nil {
		tracker = newSessionIDTracker(cfg.SessionID)
		state.sessionTracker = tracker
	} else {
		tracker.Reset(cfg.SessionID)
	}

	preferSSE := m.shouldPreferSSE(cfg)
	streamHeaders := m.mergeRequestHeaders(cfg.RequestInit)
	baseClient := cfg.HTTPClient
	streamClient := m.decorateHTTPClient(baseClient, streamHeaders, tracker, cfg.AuthProvider)

	streamableTransport := &mcp.StreamableClientTransport{
		Endpoint:   cfg.Endpoint,
		HTTPClient: streamClient,
		MaxRetries: m.resolveMaxRetries(cfg),
	}

	sseHeaders := mergeHeaders(streamHeaders, m.headersFromSSEInit(cfg.EventSourceInit))
	sseClient := m.decorateHTTPClient(baseClient, sseHeaders, tracker, cfg.AuthProvider)
	sseTransport := &mcp.SSEClientTransport{Endpoint: cfg.Endpoint, HTTPClient: sseClient}

	var streamErr error
	if !preferSSE {
		session, clientInst, err := attempt(ctx, streamableTransport)
		if err == nil {
			if session != nil {
				tracker.Set(session.ID())
			}
			state.client = clientInst
			go m.monitorSession(serverID, session, base)
			return session, nil
		}
		streamErr = err
	}
	session, clientInst, err := attempt(ctx, sseTransport)
	if err != nil {
		if streamErr != nil {
			return nil, fmt.Errorf("streamable error: %v; sse error: %w", streamErr, err)
		}
		return nil, err
	}
	if session != nil {
		tracker.Set(session.ID())
	}
	state.client = clientInst
	go m.monitorSession(serverID, session, base)
	return session, nil
}

func (m *Manager) buildStdioTransport(serverID string, cfg *StdioServerConfig) (mcp.Transport, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcpmgr: command missing for %q", serverID)
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}
	return &mcp.CommandTransport{Command: cmd}, nil
}

func (m *Manager) monitorSession(serverID string, session *mcp.ClientSession, base *BaseServerConfig) {
	if err := session.Wait(); err != nil && base.OnError != nil {
		base.OnError(err)
	}
	m.mu.Lock()
	if st, ok := m.states[serverID]; ok {
		st.session = nil
		st.client = nil
	}
	m.mu.Unlock()
}

func (m *Manager) effectiveClientName(serverID string) string {
	if m.options.DefaultClientName != "" {
		return m.options.DefaultClientName
	}
	return serverID
}

func (m *Manager) effectiveClientVersion(base *BaseServerConfig) string {
	if base.Version != "" {
		return base.Version
	}
	return m.options.DefaultClientVersion
}

func (m *Manager) composeClientOptions(serverID string, base *BaseServerConfig) mcp.ClientOptions {
	opts := m.options.DefaultClientOptions
	mergeClientOptions(&opts, &base.ClientOptions)
	wrapped := opts

	originalTool := wrapped.ToolListChangedHandler
	originalPrompt := wrapped.PromptListChangedHandler
	originalResList := wrapped.ResourceListChangedHandler
	originalResUpdate := wrapped.ResourceUpdatedHandler
	originalElicit := wrapped.ElicitationHandler

	wrapped.ToolListChangedHandler = func(ctx context.Context, req *mcp.ToolListChangedRequest) {
		if originalTool != nil {
			originalTool(ctx, req)
		}
		m.dispatchToolNotification(serverID, ctx, req)
	}
	wrapped.PromptListChangedHandler = func(ctx context.Context, req *mcp.PromptListChangedRequest) {
		if originalPrompt != nil {
			originalPrompt(ctx, req)
		}
		m.dispatchPromptNotification(serverID, ctx, req)
	}
	wrapped.ResourceListChangedHandler = func(ctx context.Context, req *mcp.ResourceListChangedRequest) {
		if originalResList != nil {
			originalResList(ctx, req)
		}
		m.dispatchResourceListNotification(serverID, ctx, req)
	}
	wrapped.ResourceUpdatedHandler = func(ctx context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
		if originalResUpdate != nil {
			originalResUpdate(ctx, req)
		}
		m.dispatchResourceUpdateNotification(serverID, ctx, req)
	}
	wrapped.ElicitationHandler = func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		if res, handled, err := m.tryElicitationHandlers(ctx, serverID, req); handled {
			return res, err
		}
		if originalElicit != nil {
			return originalElicit(ctx, req)
		}
		return nil, fmt.Errorf("elicitation not supported")
	}
	return wrapped
}

func (m *Manager) tryElicitationHandlers(ctx context.Context, serverID string, req *mcp.ElicitRequest) (*mcp.ElicitResult, bool, error) {
	if handler := m.getServerElicitationHandler(serverID); handler != nil {
		res, err := handler(ctx, req)
		return res, true, err
	}
	if handler := m.getGlobalElicitationHandler(); handler != nil {
		res, err := handler(ctx, req)
		return res, true, err
	}
	if callback := m.getGlobalElicitationCallback(); callback != nil {
		res, err := m.invokeElicitationCallback(ctx, serverID, req, callback)
		return res, true, err
	}
	return nil, false, nil
}

func (m *Manager) getServerElicitationHandler(serverID string) ElicitationHandler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if h, ok := m.serverElicitations[serverID]; ok && h != nil {
		return h
	}
	return nil
}

func (m *Manager) getGlobalElicitationHandler() ElicitationHandler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.globalElicitation
}

func (m *Manager) getGlobalElicitationCallback() GlobalElicitationCallback {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.globalElicitationFunc
}

func mergeClientOptions(dst, src *mcp.ClientOptions) {
	if src == nil {
		return
	}
	if src.CreateMessageHandler != nil {
		dst.CreateMessageHandler = src.CreateMessageHandler
	}
	if src.ElicitationHandler != nil {
		dst.ElicitationHandler = src.ElicitationHandler
	}
	if src.ToolListChangedHandler != nil {
		dst.ToolListChangedHandler = src.ToolListChangedHandler
	}
	if src.PromptListChangedHandler != nil {
		dst.PromptListChangedHandler = src.PromptListChangedHandler
	}
	if src.ResourceListChangedHandler != nil {
		dst.ResourceListChangedHandler = src.ResourceListChangedHandler
	}
	if src.ResourceUpdatedHandler != nil {
		dst.ResourceUpdatedHandler = src.ResourceUpdatedHandler
	}
	if src.LoggingMessageHandler != nil {
		dst.LoggingMessageHandler = src.LoggingMessageHandler
	}
	if src.ProgressNotificationHandler != nil {
		dst.ProgressNotificationHandler = src.ProgressNotificationHandler
	}
	if src.KeepAlive != 0 {
		dst.KeepAlive = src.KeepAlive
	}
}

func (m *Manager) resolveLogger(_ string, base *BaseServerConfig) RPCLogger {
	if base.RPCLogger != nil {
		return base.RPCLogger
	}
	if m.options.RPCLogger != nil {
		return m.options.RPCLogger
	}
	if base.LogJSONRPC || m.options.DefaultLogJSONRPC || m.defaultLogJSONRPC {
		return func(event RPCLogEvent) {
			fmt.Printf("[MCP:%s] %s %s\n", event.ServerID, strings.ToUpper(string(event.Direction)), string(event.Message))
		}
	}
	return nil
}

// DisconnectServer closes the session for the given server ID.
func (m *Manager) DisconnectServer(ctx context.Context, serverID string) error {
	m.mu.Lock()
	state, ok := m.states[serverID]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	session := state.session
	m.mu.Unlock()
	if session == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	var closeErr error
	go func() {
		closeErr = session.Close()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return closeErr
	}
}

// DisconnectAllServers closes sessions for all servers.
func (m *Manager) DisconnectAllServers(ctx context.Context) error {
	ids := m.ListServers()
	var errs []error
	for _, id := range ids {
		if err := m.DisconnectServer(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RemoveServer removes a server configuration and closes any active session.
func (m *Manager) RemoveServer(ctx context.Context, serverID string) error {
	if err := m.DisconnectServer(ctx, serverID); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.states, serverID)
	delete(m.notifications, serverID)
	delete(m.serverElicitations, serverID)
	delete(m.rawNotifications, serverID)
	handlers := append([]func(string){}, m.serverRemovedHandlers...)
	m.mu.Unlock()
	// Notify out of lock to avoid deadlocks.
	for _, h := range handlers {
		// Best-effort; isolate panics.
		func(handler func(string), id string) {
			defer func() { _ = recover() }()
			handler(id)
		}(h, serverID)
	}
	return nil
}

// OnServerRemoved registers a callback invoked after RemoveServer deletes the
// server from the manager. Handlers run without the manager lock held.
func (m *Manager) OnServerRemoved(handler func(string)) {
	if handler == nil {
		return
	}
	m.mu.Lock()
	m.serverRemovedHandlers = append(m.serverRemovedHandlers, handler)
	m.mu.Unlock()
}

// PingServer sends a protocol-level ping to the MCP server, establishing a
// connection if needed and respecting the manager's timeout configuration.
func (m *Manager) PingServer(ctx context.Context, serverID string, params *mcp.PingParams) error {
	session, _, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return err
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	return session.Ping(ctx, params)
}

// ListTools retrieves available tools and caches the metadata fields so they
// can be accessed later without another round-trip via GetAllToolsMetadata.
func (m *Manager) ListTools(ctx context.Context, serverID string, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
	session, state, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	res, err := session.ListTools(ctx, params)
	if err != nil {
		if isMethodUnavailableError(err, "tools/list") {
			m.mu.Lock()
			state.toolsMeta = make(map[string]map[string]any)
			m.mu.Unlock()
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{}}, nil
		}
		return nil, err
	}
	m.mu.Lock()
	meta := make(map[string]map[string]any)
	for _, tool := range res.Tools {
		if tool.Meta != nil {
			meta[tool.Name] = tool.Meta
		}
	}
	state.toolsMeta = meta
	m.mu.Unlock()
	return res, nil
}

// GetTools aggregates tool lists across the provided server IDs, defaulting to
// all registered servers when none are specified.
func (m *Manager) GetTools(ctx context.Context, serverIDs ...string) ([]*mcp.Tool, error) {
	ids := serverIDs
	if len(ids) == 0 {
		ids = m.ListServers()
	}
	var all []*mcp.Tool
	for _, id := range ids {
		res, err := m.ListTools(ctx, id, nil)
		if err != nil {
			return nil, err
		}
		all = append(all, res.Tools...)
	}
	return all, nil
}

// GetAllToolsMetadata returns the metadata snapshot captured during the last
// successful ListTools call for the server.
func (m *Manager) GetAllToolsMetadata(serverID string) map[string]map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if st, ok := m.states[serverID]; ok {
		copy := make(map[string]map[string]any, len(st.toolsMeta))
		for name, data := range st.toolsMeta {
			copy[name] = data
		}
		return copy
	}
	return map[string]map[string]any{}
}

// ExecuteTool invokes a tool on the specified server after ensuring a session
// is connected and applying the appropriate timeout.
func (m *Manager) ExecuteTool(ctx context.Context, serverID, toolName string, args any) (*mcp.CallToolResult, error) {
	params := &mcp.CallToolParams{Name: toolName, Arguments: args}
	return m.ExecuteToolWithParams(ctx, serverID, params)
}

// ExecuteToolWithParams invokes a tool with the provided CallToolParams,
// allowing callers to preserve metadata such as progress tokens.
func (m *Manager) ExecuteToolWithParams(ctx context.Context, serverID string, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	session, _, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return nil, err
	}
	if params == nil {
		return nil, fmt.Errorf("mcpmgr: missing call tool params for %q", serverID)
	}
	if params.Name == "" {
		return nil, fmt.Errorf("mcpmgr: tool name is required for %q", serverID)
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	return session.CallTool(ctx, params)
}

// ListResources proxies the resources/list request, coercing "method not
// found" style errors into an empty list for convenience.
func (m *Manager) ListResources(ctx context.Context, serverID string, params *mcp.ListResourcesParams) (*mcp.ListResourcesResult, error) {
	session, _, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	res, err := session.ListResources(ctx, params)
	if err != nil && isMethodUnavailableError(err, "resources/list") {
		return &mcp.ListResourcesResult{Resources: []*mcp.Resource{}}, nil
	}
	return res, err
}

// ReadResource proxies the resources/read request and honors the server's
// configured timeout.
func (m *Manager) ReadResource(ctx context.Context, serverID string, params *mcp.ReadResourceParams) (*mcp.ReadResourceResult, error) {
	session, _, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	return session.ReadResource(ctx, params)
}

// ListResourceTemplates retrieves available resource templates, returning an
// empty list when the server reports that templates are unsupported.
func (m *Manager) ListResourceTemplates(ctx context.Context, serverID string, params *mcp.ListResourceTemplatesParams) (*mcp.ListResourceTemplatesResult, error) {
	session, _, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	res, err := session.ListResourceTemplates(ctx, params)
	if err != nil && isMethodUnavailableError(err, "resources/template/list") {
		return &mcp.ListResourceTemplatesResult{ResourceTemplates: []*mcp.ResourceTemplate{}}, nil
	}
	return res, err
}

// SubscribeResource subscribes to resource updates using the manager-managed
// session.
func (m *Manager) SubscribeResource(ctx context.Context, serverID string, params *mcp.SubscribeParams) error {
	session, _, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return err
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	return session.Subscribe(ctx, params)
}

// UnsubscribeResource cancels resource subscriptions previously created via
// SubscribeResource.
func (m *Manager) UnsubscribeResource(ctx context.Context, serverID string, params *mcp.UnsubscribeParams) error {
	session, _, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return err
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	return session.Unsubscribe(ctx, params)
}

// ListPrompts retrieves server prompts, normalizing unsupported servers to an
// empty prompt slice.
func (m *Manager) ListPrompts(ctx context.Context, serverID string, params *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error) {
	session, _, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	res, err := session.ListPrompts(ctx, params)
	if err != nil && isMethodUnavailableError(err, "prompts/list") {
		return &mcp.ListPromptsResult{Prompts: []*mcp.Prompt{}}, nil
	}
	return res, err
}

// GetPrompt retrieves a single prompt definition from the target server.
func (m *Manager) GetPrompt(ctx context.Context, serverID string, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error) {
	session, _, timeout, err := m.ensureSession(ctx, serverID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := m.withTimeout(ctx, timeout)
	defer cancel()
	return session.GetPrompt(ctx, params)
}

// GetSessionID returns the negotiated session identifier for HTTP transports
// when available, which can be useful for correlating logs across services.
func (m *Manager) GetSessionID(serverID string) (string, error) {
	m.mu.RLock()
	state, ok := m.states[serverID]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("mcpmgr: unknown server %q", serverID)
	}
	session := state.session
	m.mu.RUnlock()
	if session == nil {
		return "", fmt.Errorf("mcpmgr: server %q not connected", serverID)
	}
	id := session.ID()
	if id == "" {
		return "", fmt.Errorf("mcpmgr: session ID unavailable for %q", serverID)
	}
	return id, nil
}

// Register notification handlers.

func (m *Manager) dispatchToolNotification(serverID string, ctx context.Context, req *mcp.ToolListChangedRequest) {
	if req == nil {
		return
	}
	m.mu.RLock()
	reg := m.notifications[serverID]
	var handlers []func(context.Context, *mcp.ToolListChangedRequest)
	if reg != nil {
		handlers = append(handlers, reg.toolListHandlers...)
	}
	m.mu.RUnlock()
	for _, h := range handlers {
		h(ctx, req)
	}
}

func (m *Manager) dispatchPromptNotification(serverID string, ctx context.Context, req *mcp.PromptListChangedRequest) {
	if req == nil {
		return
	}
	m.mu.RLock()
	reg := m.notifications[serverID]
	var handlers []func(context.Context, *mcp.PromptListChangedRequest)
	if reg != nil {
		handlers = append(handlers, reg.promptListHandlers...)
	}
	m.mu.RUnlock()
	for _, h := range handlers {
		h(ctx, req)
	}
}

func (m *Manager) dispatchResourceListNotification(serverID string, ctx context.Context, req *mcp.ResourceListChangedRequest) {
	if req == nil {
		return
	}
	m.mu.RLock()
	reg := m.notifications[serverID]
	var handlers []func(context.Context, *mcp.ResourceListChangedRequest)
	if reg != nil {
		handlers = append(handlers, reg.resourceListHandlers...)
	}
	m.mu.RUnlock()
	for _, h := range handlers {
		h(ctx, req)
	}
}

func (m *Manager) dispatchResourceUpdateNotification(serverID string, ctx context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
	if req == nil {
		return
	}
	m.mu.RLock()
	reg := m.notifications[serverID]
	var handlers []func(context.Context, *mcp.ResourceUpdatedNotificationRequest)
	if reg != nil {
		handlers = append(handlers, reg.resourceUpdateHandlers...)
	}
	m.mu.RUnlock()
	for _, h := range handlers {
		h(ctx, req)
	}
}

// OnToolListChanged registers a handler for tool list notifications.
func (m *Manager) OnToolListChanged(serverID string, handler func(context.Context, *mcp.ToolListChangedRequest)) {
	m.mu.Lock()
	reg := m.ensureRegistryLocked(serverID)
	reg.toolListHandlers = append(reg.toolListHandlers, handler)
	m.mu.Unlock()
}

// OnPromptListChanged registers a handler for prompt list notifications.
func (m *Manager) OnPromptListChanged(serverID string, handler func(context.Context, *mcp.PromptListChangedRequest)) {
	m.mu.Lock()
	reg := m.ensureRegistryLocked(serverID)
	reg.promptListHandlers = append(reg.promptListHandlers, handler)
	m.mu.Unlock()
}

// OnResourceListChanged registers a handler for resource list notifications.
func (m *Manager) OnResourceListChanged(serverID string, handler func(context.Context, *mcp.ResourceListChangedRequest)) {
	m.mu.Lock()
	reg := m.ensureRegistryLocked(serverID)
	reg.resourceListHandlers = append(reg.resourceListHandlers, handler)
	m.mu.Unlock()
}

// OnResourceUpdated registers a handler for resource updated notifications.
func (m *Manager) OnResourceUpdated(serverID string, handler func(context.Context, *mcp.ResourceUpdatedNotificationRequest)) {
	m.mu.Lock()
	reg := m.ensureRegistryLocked(serverID)
	reg.resourceUpdateHandlers = append(reg.resourceUpdateHandlers, handler)
	m.mu.Unlock()
}

// AddNotificationHandler registers a handler for the provided notification
// schema, mirroring the flexibility of the TypeScript client manager.
func (m *Manager) AddNotificationHandler(serverID string, schema NotificationSchema, handler NotificationHandlerFunc) {
	if handler == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rawNotifications[serverID]; !ok {
		m.rawNotifications[serverID] = make(map[NotificationSchema][]NotificationHandlerFunc)
	}
	m.rawNotifications[serverID][schema] = append(m.rawNotifications[serverID][schema], handler)
}

func (m *Manager) dispatchRawNotification(ctx context.Context, serverID string, schema NotificationSchema, req mcp.Request) {
	m.mu.RLock()
	serverHandlers := m.rawNotifications[serverID]
	handlers := serverHandlers[schema]
	if len(handlers) == 0 {
		m.mu.RUnlock()
		return
	}
	copyHandlers := append([]NotificationHandlerFunc(nil), handlers...)
	m.mu.RUnlock()
	payload := NotificationPayload{ServerID: serverID, Method: schema, Request: req}
	for _, h := range copyHandlers {
		h := h
		func() {
			defer func() {
				if recover() != nil {
					// Ignore panics from individual handlers to keep other listeners alive.
				}
			}()
			h(ctx, payload)
		}()
	}
}

func (m *Manager) notificationMiddleware(serverID string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "" {
				m.dispatchRawNotification(ctx, serverID, NotificationSchema(method), req)
			}
			return next(ctx, method, req)
		}
	}
}

// SetElicitationHandler sets a server-specific elicitation handler.
func (m *Manager) SetElicitationHandler(serverID string, handler ElicitationHandler) {
	m.mu.Lock()
	m.serverElicitations[serverID] = handler
	m.mu.Unlock()
}

// ClearElicitationHandler removes a server-specific handler.
func (m *Manager) ClearElicitationHandler(serverID string) {
	m.SetElicitationHandler(serverID, nil)
}

// SetGlobalElicitationHandler sets the fallback elicitation handler.
func (m *Manager) SetGlobalElicitationHandler(handler ElicitationHandler) {
	m.mu.Lock()
	m.globalElicitation = handler
	m.mu.Unlock()
}

// ClearGlobalElicitationHandler clears the fallback elicitation handler.
func (m *Manager) ClearGlobalElicitationHandler() {
	m.SetGlobalElicitationHandler(nil)
}

// SetElicitationCallback configures a global callback that receives every
// elicitation request lacking a server-specific handler. Returning a non-nil
// result responds immediately; returning nil defers completion until
// RespondToElicitation is invoked.
func (m *Manager) SetElicitationCallback(callback GlobalElicitationCallback) {
	m.mu.Lock()
	m.globalElicitationFunc = callback
	m.mu.Unlock()
}

// ClearElicitationCallback removes the global callback.
func (m *Manager) ClearElicitationCallback() {
	m.SetElicitationCallback(nil)
}

// GetPendingElicitations returns a snapshot of outstanding elicitation
// requests, keyed by request ID.
func (m *Manager) GetPendingElicitations() map[string]PendingElicitationInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snapshot := make(map[string]PendingElicitationInfo, len(m.pendingElicitations))
	for id, pending := range m.pendingElicitations {
		snapshot[id] = PendingElicitationInfo{Event: pending.event}
	}
	return snapshot
}

// RespondToElicitation fulfills a pending elicitation request identified by
// requestID.
func (m *Manager) RespondToElicitation(requestID string, result *mcp.ElicitResult) bool {
	pending := m.removePendingElicitation(requestID)
	if pending == nil {
		return false
	}
	pending.resolve(result)
	return true
}

func (m *Manager) ensureRegistryLocked(serverID string) *notificationRegistry {
	reg := m.notifications[serverID]
	if reg == nil {
		reg = &notificationRegistry{}
		m.notifications[serverID] = reg
	}
	return reg
}

// GetConnectionStatusByAttemptingPing attempts a ping to determine whether the
// session is connected, currently connecting, or disconnected.
func (m *Manager) GetConnectionStatusByAttemptingPing(ctx context.Context, serverID string) ConnectionStatus {
	m.mu.RLock()
	state, ok := m.states[serverID]
	if !ok {
		m.mu.RUnlock()
		return StatusDisconnected
	}
	if state.connecting {
		m.mu.RUnlock()
		return StatusConnecting
	}
	session := state.session
	m.mu.RUnlock()
	if session == nil {
		return StatusDisconnected
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := session.Ping(ctx, nil); err != nil {
		return StatusDisconnected
	}
	return StatusConnected
}

func (m *Manager) ensureSession(ctx context.Context, serverID string) (*mcp.ClientSession, *managedState, time.Duration, error) {
	for {
		m.mu.RLock()
		state, ok := m.states[serverID]
		if !ok {
			m.mu.RUnlock()
			return nil, nil, 0, fmt.Errorf("mcpmgr: unknown server %q", serverID)
		}
		if state.session != nil {
			session := state.session
			timeout := state.timeout
			m.mu.RUnlock()
			return session, state, timeout, nil
		}
		connectCh := state.connectCh
		connecting := state.connecting
		m.mu.RUnlock()
		if !connecting {
			if _, err := m.ConnectToServer(ctx, serverID, nil); err != nil {
				return nil, nil, 0, err
			}
			continue
		}
		if connectCh == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil, nil, 0, ctx.Err()
		case <-connectCh:
		}
	}
}

func (m *Manager) withTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

type loggingTransport struct {
	serverID string
	delegate mcp.Transport
	logger   RPCLogger
}

func (t *loggingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.delegate.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &loggingConnection{serverID: t.serverID, delegate: conn, logger: t.logger}, nil
}

type loggingConnection struct {
	serverID string
	delegate mcp.Connection
	logger   RPCLogger
	mu       sync.Mutex
}

func (c *loggingConnection) SessionID() string { return c.delegate.SessionID() }

func (c *loggingConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	msg, err := c.delegate.Read(ctx)
	if err == nil {
		c.emit(RPCDirectionReceive, msg)
	}
	return msg, err
}

func (c *loggingConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	if err := c.delegate.Write(ctx, msg); err != nil {
		return err
	}
	c.emit(RPCDirectionSend, msg)
	return nil
}

func (c *loggingConnection) Close() error { return c.delegate.Close() }

func (c *loggingConnection) emit(direction RPCDirection, msg jsonrpc.Message) {
	if c.logger == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	encoded, err := json.Marshal(msg)
	if err != nil {
		encoded = []byte(err.Error())
	}
	c.logger(RPCLogEvent{Direction: direction, Message: encoded, ServerID: c.serverID})
}

func isMethodUnavailableError(err error, method string) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	if !(strings.Contains(lower, "method not found") ||
		strings.Contains(lower, "not implemented") ||
		strings.Contains(lower, "unsupported") ||
		strings.Contains(lower, "does not support") ||
		strings.Contains(lower, "unimplemented")) {
		return false
	}
	method = strings.ToLower(method)
	if strings.Contains(lower, method) {
		return true
	}
	for _, part := range strings.FieldsFunc(method, func(r rune) bool {
		return r == '/' || r == ':' || r == '.' || r == '_' || r == '-'
	}) {
		if part != "" && strings.Contains(lower, part) {
			return true
		}
	}
	return true
}
func (m *Manager) invokeElicitationCallback(ctx context.Context, serverID string, req *mcp.ElicitRequest, callback GlobalElicitationCallback) (*mcp.ElicitResult, error) {
	params := req.Params
	message := ""
	var schema any
	if params != nil {
		message = params.Message
		schema = params.RequestedSchema
	}
	event := ElicitationEvent{
		ServerID:  serverID,
		RequestID: generateElicitationRequestID(),
		Message:   message,
		Schema:    schema,
		Params:    req,
		CreatedAt: time.Now(),
	}
	pending := newPendingElicitation(event)
	m.mu.Lock()
	m.pendingElicitations[event.RequestID] = pending
	m.mu.Unlock()

	result, err := callback(ctx, &event)
	if err != nil {
		m.removePendingElicitation(event.RequestID)
		pending.resolve(nil)
		return nil, err
	}
	if result != nil {
		pending.resolve(result)
	}
	res, waitErr := pending.await(ctx)
	m.removePendingElicitation(event.RequestID)
	if waitErr != nil {
		return nil, waitErr
	}
	return res, nil
}

func (m *Manager) removePendingElicitation(requestID string) *pendingElicitation {
	m.mu.Lock()
	defer m.mu.Unlock()
	pending, ok := m.pendingElicitations[requestID]
	if ok {
		delete(m.pendingElicitations, requestID)
	}
	return pending
}

func generateElicitationRequestID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 8)
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return fmt.Sprintf("elicit_%d", time.Now().UnixNano())
		}
		buf[i] = alphabet[n.Int64()]
	}
	return fmt.Sprintf("elicit_%d_%s", time.Now().UnixNano(), string(buf))
}

type pendingElicitation struct {
	event  ElicitationEvent
	result chan *mcp.ElicitResult
}

func newPendingElicitation(event ElicitationEvent) *pendingElicitation {
	return &pendingElicitation{event: event, result: make(chan *mcp.ElicitResult, 1)}
}

func (p *pendingElicitation) resolve(res *mcp.ElicitResult) {
	select {
	case p.result <- res:
	default:
	}
}

func (p *pendingElicitation) await(ctx context.Context) (*mcp.ElicitResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-p.result:
		return res, nil
	}
}

type sessionIDTracker struct {
	mu    sync.RWMutex
	value string
}

func newSessionIDTracker(initial string) *sessionIDTracker {
	return &sessionIDTracker{value: initial}
}

func (s *sessionIDTracker) Set(value string) {
	s.mu.Lock()
	s.value = value
	s.mu.Unlock()
}

func (s *sessionIDTracker) Reset(value string) { s.Set(value) }

func (s *sessionIDTracker) Value() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

func (m *Manager) shouldPreferSSE(cfg *HTTPServerConfig) bool {
	if cfg.PreferSSE != nil {
		return *cfg.PreferSSE
	}
	return strings.HasSuffix(strings.TrimSpace(cfg.Endpoint), "/sse")
}

func (m *Manager) mergeRequestHeaders(init *HTTPRequestInit) http.Header {
	if init == nil {
		return nil
	}
	return cloneHeader(init.Headers)
}

func (m *Manager) headersFromSSEInit(init *SSERequestInit) http.Header {
	if init == nil {
		return nil
	}
	return cloneHeader(init.Headers)
}

func (m *Manager) decorateHTTPClient(base *http.Client, headers http.Header, tracker *sessionIDTracker, provider HTTPAuthProvider) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	clone := *base
	clone.Transport = &headerDecorator{
		next:         defaultRoundTripper(base.Transport),
		headers:      cloneHeader(headers),
		tracker:      tracker,
		authProvider: provider,
	}
	return &clone
}

func (m *Manager) resolveMaxRetries(cfg *HTTPServerConfig) int {
	if cfg.ReconnectionOptions != nil && cfg.ReconnectionOptions.MaxRetries != 0 {
		return cfg.ReconnectionOptions.MaxRetries
	}
	return cfg.MaxRetries
}

func mergeHeaders(headers ...http.Header) http.Header {
	result := http.Header{}
	for _, hdr := range headers {
		if len(hdr) == 0 {
			continue
		}
		for k, values := range hdr {
			result[k] = append([]string(nil), values...)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func cloneHeader(h http.Header) http.Header {
	if len(h) == 0 {
		return nil
	}
	clone := make(http.Header, len(h))
	for k, values := range h {
		clone[k] = append([]string(nil), values...)
	}
	return clone
}

type headerDecorator struct {
	next         http.RoundTripper
	headers      http.Header
	tracker      *sessionIDTracker
	authProvider HTTPAuthProvider
}

func (d *headerDecorator) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if len(d.headers) > 0 {
		for k, values := range d.headers {
			req.Header.Del(k)
			for _, v := range values {
				req.Header.Add(k, v)
			}
		}
	}
	if d.tracker != nil {
		if sessionID := d.tracker.Value(); sessionID != "" {
			req.Header.Set(sessionIDHeaderName, sessionID)
		}
	}
	if d.authProvider != nil && req.Header.Get("Authorization") == "" {
		token, err := d.authProvider(req.Context())
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", token)
		}
	}
	return d.next.RoundTrip(req)
}

func defaultRoundTripper(next http.RoundTripper) http.RoundTripper {
	if next != nil {
		return next
	}
	return http.DefaultTransport
}
