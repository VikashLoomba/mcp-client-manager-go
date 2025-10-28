package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/cors"
	"github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

// Gateway exposes a Streamable MCP server that fronts every server managed by
// mcpmgr under a single HTTP endpoint.
type Gateway struct {
    manager *mcpmgr.Manager
    opts    Options

	features *featureIndex

	progress *progressTracker

	server            *mcp.Server
	StreamHandler     *mcp.StreamableHTTPHandler
	streamHTTPHandler http.Handler
    httpHandler       http.Handler

    serverMu     sync.Mutex
    httpServerMu sync.Mutex
    httpServer   *http.Server

    registerMu      sync.Mutex
    registeredSrvID map[string]struct{}

    // uiRoots mirrors the last-known UI roots set by the upstream client.
    // Keys are canonical root URIs; values are the full root descriptors.
    rootsMu sync.RWMutex
    uiRoots map[string]*mcp.Root
}

// Options returns the gateway's resolved options, including defaults applied
// during construction.
func (g *Gateway) Options() Options {
	return g.opts
}

// NewGateway builds a Gateway, synchronizes the initial feature snapshot, and
// registers change watchers for every known server.
func NewGateway(mgr *mcpmgr.Manager, opts *Options) (*Gateway, error) {
	if mgr == nil {
		return nil, fmt.Errorf("mcpgateway: manager is required")
	}
	options := opts.withDefaults()
    g := &Gateway{
        manager:         mgr,
        opts:            options,
        features:        newFeatureIndex(options.Namespace),
        registeredSrvID: make(map[string]struct{}),
        progress:        newProgressTracker(options.Logger),
        uiRoots:         make(map[string]*mcp.Root),
    }

    g.server = mcp.NewServer(options.Implementation, &mcp.ServerOptions{
        HasTools:           true,
        HasPrompts:         true,
        HasResources:       true,
        SubscribeHandler:   g.handleSubscribe,
        UnsubscribeHandler: g.handleUnsubscribe,
    })
	g.StreamHandler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return g.server
	}, &options.Streamable)
	handler := http.Handler(g.StreamHandler)
	if options.TokenVerifier != nil {
		handler = auth.RequireBearerToken(options.TokenVerifier, options.TokenOptions)(handler)
	} else if options.TokenOptions != nil {
		return nil, fmt.Errorf("mcpgateway: TokenOptions specified without TokenVerifier")
	}
	g.streamHTTPHandler = handler

	if options.TokenVerifier != nil && options.TokenOptions != nil {
		metadataMux := http.NewServeMux()
		metadataHandler := newOAuthProtectedResourceHandler(options)
		metadataMux.Handle("/.well-known/oauth-protected-resource", metadataHandler)
		metadataMux.Handle("/.well-known/oauth-protected-resource/", metadataHandler)
		g.mountPaths(metadataMux, handler)
		g.httpHandler = metadataMux
	} else {
		g.httpHandler = g.mountHandler(handler)
	}

    mgr.SetElicitationCallback(g.forwardElicitation)

    // React when servers are removed from the manager by pruning gateway state.
    mgr.OnServerRemoved(func(serverID string) {
        go func(id string) {
            _ = g.DetachServer(context.Background(), id)
        }(serverID)
    })

    for _, serverID := range mgr.ListServers() {
        g.registerServerHooks(serverID)
    }
	if options.AutoConnect {
		ctx := context.Background()
		for _, serverID := range mgr.ListServers() {
			if _, err := mgr.ConnectToServer(ctx, serverID, nil); err != nil {
				options.Logger.Warn("autoconnect failed", "server", serverID, "error", err)
			}
		}
	}
	if err := g.SyncAll(context.Background()); err != nil {
		return nil, err
	}

	return g, nil
}

// Handler exposes the HTTP handler that serves the Streamable endpoint.
func (g *Gateway) Handler() http.Handler {
    return g.httpHandler
}

// ServeMux returns the underlying *http.ServeMux used by the gateway's
// Handler, creating one if necessary. This enables consumers to register
// additional routes on the same server process before calling ListenAndServe.
//
// If authentication is configured via Options.TokenVerifier, the returned mux
// will already include the OAuth Protected Resource metadata endpoints as well
// as the Streamable MCP endpoint mounted at Options.Path. Additional routes are
// not automatically wrapped with authentication middleware; callers are
// responsible for protecting them as desired.
func (g *Gateway) ServeMux() *http.ServeMux {
    if mux, ok := g.httpHandler.(*http.ServeMux); ok {
        return mux
    }
    // Wrap the current handler in a mux and mount the stream handler according
    // to the configured path so callers can add their own routes.
    mux := http.NewServeMux()
    g.mountPaths(mux, g.streamHTTPHandler)
    g.httpHandler = mux
    return mux
}

// ListenAndServe runs an HTTP server until the provided context is cancelled or
// the server stops.
func (g *Gateway) ListenAndServe(ctx context.Context) error {
    g.httpServerMu.Lock()
    if g.httpServer != nil {
        serv := g.httpServer
        g.httpServerMu.Unlock()
        return fmt.Errorf("mcpgateway: server already running on %s", serv.Addr)
    }
    handler := g.Handler()
    srv := &http.Server{Addr: g.opts.Addr, Handler: handler}
    g.httpServer = srv
    g.httpServerMu.Unlock()
    defer func() {
        g.httpServerMu.Lock()
        if g.httpServer == srv {
            g.httpServer = nil
        }
        g.httpServerMu.Unlock()
    }()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), g.opts.SyncTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// ListenAndServeServer starts serving using a caller-provided *http.Server.
// If srv.Handler is nil, the gateway's Handler() is installed. This allows
// callers to customize TLS and server timeouts while reusing the gateway's
// routing and state. The server pointer is stored and returned by HTTPServer().
func (g *Gateway) ListenAndServeServer(ctx context.Context, srv *http.Server) error {
    if srv == nil {
        return fmt.Errorf("mcpgateway: nil http.Server passed to ListenAndServeServer")
    }
    g.httpServerMu.Lock()
    if g.httpServer != nil {
        serv := g.httpServer
        g.httpServerMu.Unlock()
        return fmt.Errorf("mcpgateway: server already running on %s", serv.Addr)
    }
    if srv.Handler == nil {
        srv.Handler = g.Handler()
    }
    g.httpServer = srv
    g.httpServerMu.Unlock()
    defer func() {
        g.httpServerMu.Lock()
        if g.httpServer == srv {
            g.httpServer = nil
        }
        g.httpServerMu.Unlock()
    }()

    errCh := make(chan error, 1)
    go func() {
        errCh <- srv.ListenAndServe()
    }()

    select {
    case <-ctx.Done():
        shutdownCtx, cancel := context.WithTimeout(context.Background(), g.opts.SyncTimeout)
        defer cancel()
        _ = srv.Shutdown(shutdownCtx)
        return ctx.Err()
    case err := <-errCh:
        if errors.Is(err, http.ErrServerClosed) {
            return nil
        }
        return err
    }
}

// Shutdown stops the embedded HTTP server if it is running.
func (g *Gateway) Shutdown(ctx context.Context) error {
    g.httpServerMu.Lock()
    srv := g.httpServer
    g.httpServer = nil
    g.httpServerMu.Unlock()
    if srv == nil {
        return nil
    }
    if ctx == nil {
        ctx = context.Background()
    }
    return srv.Shutdown(ctx)
}

// HTTPServer returns the active *http.Server if the gateway was started with
// ListenAndServe or ListenAndServeServer. It returns nil if the server is not
// running. The returned pointer must be treated as read-only by callers.
func (g *Gateway) HTTPServer() *http.Server {
    g.httpServerMu.Lock()
    defer g.httpServerMu.Unlock()
    return g.httpServer
}

// IsServing reports whether ListenAndServe or ListenAndServeServer has started
// an HTTP server that is still active.
func (g *Gateway) IsServing() bool {
    g.httpServerMu.Lock()
    defer g.httpServerMu.Unlock()
    return g.httpServer != nil
}

// SyncAll refreshes every known server.
func (g *Gateway) SyncAll(ctx context.Context) error {
	var lastErr error
	for _, serverID := range g.manager.ListServers() {
		if err := g.SyncServer(ctx, serverID); err != nil {
			lastErr = err
			g.logError("sync server", err, "server", serverID)
		}
	}
	return lastErr
}

// SyncServer refreshes a specific server's tools, prompts, and resources.
func (g *Gateway) SyncServer(ctx context.Context, serverID string) error {
	if err := g.syncTools(ctx, serverID); err != nil {
		return err
	}
	if err := g.syncPrompts(ctx, serverID); err != nil {
		return err
	}
	if err := g.syncResources(ctx, serverID); err != nil {
		return err
	}
    return g.syncResourceTemplates(ctx, serverID)
}

// AttachServer registers hooks and synchronizes a server that was added to the
// manager after the gateway was constructed.
func (g *Gateway) AttachServer(ctx context.Context, serverID string, cfg mcpmgr.ServerConfig) error {
	if cfg != nil {
		if _, err := g.manager.ConnectToServer(ctx, serverID, cfg); err != nil {
			return err
		}
	} else if g.opts.AutoConnect {
		if _, err := g.manager.ConnectToServer(ctx, serverID, nil); err != nil {
			return err
		}
    }
    g.registerServerHooks(serverID)
    // Ensure any cached UI roots are applied to the newly attached server.
    g.applyCachedRootsToServer(serverID)
    return g.SyncServer(ctx, serverID)
}

// DetachServer removes all features for the given server from the gateway and
// forgets its registration hooks. It does not modify the manager state.
func (g *Gateway) DetachServer(ctx context.Context, serverID string) error {
    // Remove aggregated features first so the UI no longer sees them.
    removedTools, removedPrompts, removedResources, removedTemplates := g.features.RemoveAllForServer(serverID)
    g.serverMu.Lock()
    if len(removedTools) > 0 {
        g.server.RemoveTools(removedTools...)
    }
    if len(removedPrompts) > 0 {
        g.server.RemovePrompts(removedPrompts...)
    }
    if len(removedResources) > 0 {
        g.server.RemoveResources(removedResources...)
    }
    if len(removedTemplates) > 0 {
        g.server.RemoveResourceTemplates(removedTemplates...)
    }
    g.serverMu.Unlock()

    // Allow re-attachment by clearing our seen set.
    g.registerMu.Lock()
    delete(g.registeredSrvID, serverID)
    g.registerMu.Unlock()
    return nil
}

// RemoveServer detaches the server from the gateway and removes it from the manager.
func (g *Gateway) RemoveServer(ctx context.Context, serverID string) error {
    // Detach first to stop exposing features immediately.
    _ = g.DetachServer(ctx, serverID)
    return g.manager.RemoveServer(ctx, serverID)
}

func (g *Gateway) syncTools(ctx context.Context, serverID string) error {
	ctx, cancel := g.syncContext(ctx)
	defer cancel()
	res, err := g.manager.ListTools(ctx, serverID, nil)
	if err != nil {
		return err
	}
	var tools []*mcp.Tool
	if res != nil {
		tools = res.Tools
	}
	removed, added := g.features.UpdateTools(serverID, tools)
	g.serverMu.Lock()
	if len(removed) > 0 {
		g.server.RemoveTools(removed...)
	}
	for _, reg := range added {
		g.server.AddTool(reg.Tool, g.makeToolHandler(reg.Target))
	}
	g.serverMu.Unlock()
	return nil
}

func (g *Gateway) syncPrompts(ctx context.Context, serverID string) error {
	ctx, cancel := g.syncContext(ctx)
	defer cancel()
	res, err := g.manager.ListPrompts(ctx, serverID, nil)
	if err != nil {
		return err
	}
	var prompts []*mcp.Prompt
	if res != nil {
		prompts = res.Prompts
	}
	removed, added := g.features.UpdatePrompts(serverID, prompts)
	g.serverMu.Lock()
	if len(removed) > 0 {
		g.server.RemovePrompts(removed...)
	}
	for _, reg := range added {
		g.server.AddPrompt(reg.Prompt, g.makePromptHandler(reg.Target))
	}
	g.serverMu.Unlock()
	return nil
}

func (g *Gateway) syncResources(ctx context.Context, serverID string) error {
	ctx, cancel := g.syncContext(ctx)
	defer cancel()
	res, err := g.manager.ListResources(ctx, serverID, nil)
	if err != nil {
		return err
	}
	var resources []*mcp.Resource
	if res != nil {
		resources = res.Resources
	}
	removed, added := g.features.UpdateResources(serverID, resources)
	g.serverMu.Lock()
	if len(removed) > 0 {
		g.server.RemoveResources(removed...)
	}
	for _, reg := range added {
		g.server.AddResource(reg.Resource, g.makeResourceHandler(reg.Target))
	}
	g.serverMu.Unlock()
	return nil
}

func (g *Gateway) syncResourceTemplates(ctx context.Context, serverID string) error {
	ctx, cancel := g.syncContext(ctx)
	defer cancel()
	res, err := g.manager.ListResourceTemplates(ctx, serverID, nil)
	if err != nil {
		return err
	}
	var templates []*mcp.ResourceTemplate
	if res != nil {
		templates = res.ResourceTemplates
	}
	removed, added := g.features.UpdateResourceTemplates(serverID, templates)
	g.serverMu.Lock()
	if len(removed) > 0 {
		g.server.RemoveResourceTemplates(removed...)
	}
	for _, reg := range added {
		g.server.AddResourceTemplate(reg.Template, g.makeResourceTemplateHandler(reg.Target))
	}
	g.serverMu.Unlock()
	return nil
}

func (g *Gateway) registerServerHooks(serverID string) {
	g.registerMu.Lock()
	if _, ok := g.registeredSrvID[serverID]; ok {
		g.registerMu.Unlock()
		return
	}
	g.registeredSrvID[serverID] = struct{}{}
	g.registerMu.Unlock()

	g.manager.OnToolListChanged(serverID, func(context.Context, *mcp.ToolListChangedRequest) {
		go g.syncAndLog("tools", serverID, g.syncTools(context.Background(), serverID))
	})
	g.manager.OnPromptListChanged(serverID, func(context.Context, *mcp.PromptListChangedRequest) {
		go g.syncAndLog("prompts", serverID, g.syncPrompts(context.Background(), serverID))
	})
	g.manager.OnResourceListChanged(serverID, func(context.Context, *mcp.ResourceListChangedRequest) {
		go func() {
			if err := g.syncResources(context.Background(), serverID); err != nil {
				g.logError("sync resources", err, "server", serverID)
			}
			if err := g.syncResourceTemplates(context.Background(), serverID); err != nil {
				g.logError("sync resource templates", err, "server", serverID)
			}
		}()
	})
	g.manager.OnResourceUpdated(serverID, g.forwardResourceUpdate(serverID))
    g.manager.AddNotificationHandler(serverID, mcpmgr.NotificationSchemaProgress, g.forwardProgress(serverID))
}

func (g *Gateway) syncAndLog(kind, serverID string, err error) {
	if err != nil {
		g.logError("sync "+kind, err, "server", serverID)
	}
}

func (g *Gateway) makeToolHandler(target toolTarget) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		session := req.Session
		callCtx := bindSession(ctx, session)
		params := &mcp.CallToolParams{Name: target.NativeName}
		if req != nil && req.Params != nil {
			params.Meta = cloneMeta(req.Params.Meta)
			params.Arguments = req.Params.Arguments
		}
		cleanup := g.trackProgressForParams(target.ServerID, session, params)
		defer cleanup()
		return g.manager.ExecuteToolWithParams(callCtx, target.ServerID, params)
	}
}

func (g *Gateway) makePromptHandler(target promptTarget) mcp.PromptHandler {
	return func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		session := req.Session
		callCtx := bindSession(ctx, session)
		params := &mcp.GetPromptParams{Name: target.NativeName}
		if req != nil && req.Params != nil {
			params.Meta = cloneMeta(req.Params.Meta)
			if len(req.Params.Arguments) > 0 {
				params.Arguments = maps.Clone(req.Params.Arguments)
			}
		}
		cleanup := g.trackProgressForParams(target.ServerID, session, params)
		defer cleanup()
		return g.manager.GetPrompt(callCtx, target.ServerID, params)
	}
}

func (g *Gateway) makeResourceHandler(target resourceTarget) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		session := req.Session
		callCtx := bindSession(ctx, session)
		params := &mcp.ReadResourceParams{URI: target.NativeURI}
		if req != nil && req.Params != nil {
			params.Meta = cloneMeta(req.Params.Meta)
		}
		cleanup := g.trackProgressForParams(target.ServerID, session, params)
		defer cleanup()
		return g.manager.ReadResource(callCtx, target.ServerID, params)
	}
}

func (g *Gateway) makeResourceTemplateHandler(target resourceTemplateTarget) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		session := req.Session
		callCtx := bindSession(ctx, session)
		native := target.NativeURI
		if req != nil && req.Params != nil {
			if candidate, ok := g.opts.Namespace.NativeResourceTemplateURI(target.ServerID, req.Params.URI); ok {
				native = candidate
			}
		}
		params := &mcp.ReadResourceParams{URI: native}
		if req != nil && req.Params != nil {
			params.Meta = cloneMeta(req.Params.Meta)
		}
		cleanup := g.trackProgressForParams(target.ServerID, session, params)
		defer cleanup()
		return g.manager.ReadResource(callCtx, target.ServerID, params)
	}
}

func (g *Gateway) handleSubscribe(ctx context.Context, req *mcp.SubscribeRequest) error {
	if req == nil || req.Params == nil {
		return fmt.Errorf("mcpgateway: missing subscribe params")
	}
	target, ok := g.features.ResourceTarget(req.Params.URI)
	if !ok {
		return fmt.Errorf("mcpgateway: unknown resource %q", req.Params.URI)
	}
	params := &mcp.SubscribeParams{URI: target.NativeURI}
	return g.manager.SubscribeResource(bindSession(ctx, req.Session), target.ServerID, params)
}

func (g *Gateway) handleUnsubscribe(ctx context.Context, req *mcp.UnsubscribeRequest) error {
	if req == nil || req.Params == nil {
		return fmt.Errorf("mcpgateway: missing unsubscribe params")
	}
	target, ok := g.features.ResourceTarget(req.Params.URI)
	if !ok {
		return fmt.Errorf("mcpgateway: unknown resource %q", req.Params.URI)
	}
	params := &mcp.UnsubscribeParams{URI: target.NativeURI}
	return g.manager.UnsubscribeResource(bindSession(ctx, req.Session), target.ServerID, params)
}

func (g *Gateway) forwardResourceUpdate(serverID string) func(context.Context, *mcp.ResourceUpdatedNotificationRequest) {
	return func(ctx context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
		if req == nil || req.Params == nil {
			return
		}
		gatewayURI, ok := g.features.ResourceTargetByNative(serverID, req.Params.URI)
		if !ok {
			if err := g.syncResources(context.Background(), serverID); err != nil {
				g.logError("resync unknown resource", err, "server", serverID)
				return
			}
			gatewayURI, ok = g.features.ResourceTargetByNative(serverID, req.Params.URI)
			if !ok {
				return
			}
		}
		params := *req.Params
		params.URI = gatewayURI
		if err := g.server.ResourceUpdated(ctx, &params); err != nil {
			g.logError("forward resource update", err, "server", serverID)
		}
	}
}

func (g *Gateway) forwardProgress(serverID string) mcpmgr.NotificationHandlerFunc {
	return func(ctx context.Context, payload mcpmgr.NotificationPayload) {
		clientReq, ok := payload.Request.(*mcp.ProgressNotificationClientRequest)
		if !ok || clientReq == nil || clientReq.Params == nil {
			return
		}
		sink := g.lookupProgressRecipient(serverID, clientReq.Params.ProgressToken)
		if sink == nil {
			return
		}
		if err := sink.NotifyProgress(ctx, clientReq.Params); err != nil {
			g.logError("forward progress", err, "server", serverID)
		}
	}
}

func (g *Gateway) forwardElicitation(ctx context.Context, event *mcpmgr.ElicitationEvent) (*mcp.ElicitResult, error) {
	session := sessionFromContext(ctx)
	if session == nil {
		return nil, fmt.Errorf("mcpgateway: no downstream session for elicitation from %s", event.ServerID)
	}
	if event == nil || event.Params == nil || event.Params.Params == nil {
		return nil, fmt.Errorf("mcpgateway: malformed elicitation payload")
	}
	return session.Elicit(ctx, event.Params.Params)
}

// SetUIRoots replaces the cached UI roots with the provided set and propagates
// the additions/removals to all downstream servers.
func (g *Gateway) SetUIRoots(roots []*mcp.Root) {
    newSet := make(map[string]*mcp.Root, len(roots))
    for _, r := range roots {
        if r == nil || r.URI == "" {
            continue
        }
        newSet[r.URI] = r
    }
    g.rootsMu.Lock()
    added := make([]*mcp.Root, 0, len(newSet))
    removed := make([]string, 0)
    for uri, root := range newSet {
        if prev, ok := g.uiRoots[uri]; !ok || !rootsEqual(prev, root) {
            added = append(added, root)
        }
    }
    for uri := range g.uiRoots {
        if _, ok := newSet[uri]; !ok {
            removed = append(removed, uri)
        }
    }
    g.uiRoots = newSet
    g.rootsMu.Unlock()
    if len(added) > 0 || len(removed) > 0 {
        g.propagateRootsChanges(added, removed)
    }
}

// AddUIRoots adds roots to the cached set and propagates them downstream.
func (g *Gateway) AddUIRoots(roots ...*mcp.Root) {
    if len(roots) == 0 {
        return
    }
    g.rootsMu.Lock()
    var toAdd []*mcp.Root
    for _, r := range roots {
        if r == nil || r.URI == "" {
            continue
        }
        if prev, ok := g.uiRoots[r.URI]; !ok || !rootsEqual(prev, r) {
            g.uiRoots[r.URI] = r
            toAdd = append(toAdd, r)
        }
    }
    g.rootsMu.Unlock()
    if len(toAdd) > 0 {
        g.propagateRootsChanges(toAdd, nil)
    }
}

// RemoveUIRoots removes the specified root URIs from the cached set and
// propagates the removals downstream.
func (g *Gateway) RemoveUIRoots(uris ...string) {
    if len(uris) == 0 {
        return
    }
    g.rootsMu.Lock()
    var toRemove []string
    for _, uri := range uris {
        if _, ok := g.uiRoots[uri]; ok {
            delete(g.uiRoots, uri)
            toRemove = append(toRemove, uri)
        }
    }
    g.rootsMu.Unlock()
    if len(toRemove) > 0 {
        g.propagateRootsChanges(nil, toRemove)
    }
}

// propagateRootsChanges pushes adds/removes to all known downstream clients.
func (g *Gateway) propagateRootsChanges(added []*mcp.Root, removed []string) {
    for _, serverID := range g.manager.ListServers() {
        client := g.manager.GetClient(serverID)
        if client == nil {
            // No client yet; changes will be applied on attach/connect.
            continue
        }
        if len(removed) > 0 {
            // Best-effort; errors are logged inside the SDK or ignored.
            client.RemoveRoots(removed...)
        }
        if len(added) > 0 {
            client.AddRoots(added...)
        }
    }
}

// applyCachedRootsToServer applies current UI roots to a specific server client.
func (g *Gateway) applyCachedRootsToServer(serverID string) {
    client := g.manager.GetClient(serverID)
    if client == nil {
        return
    }
    g.rootsMu.RLock()
    if len(g.uiRoots) == 0 {
        g.rootsMu.RUnlock()
        return
    }
    // Apply as a set; AddRoots is idempotent by URI.
    roots := make([]*mcp.Root, 0, len(g.uiRoots))
    for _, r := range g.uiRoots {
        roots = append(roots, r)
    }
    g.rootsMu.RUnlock()
    if len(roots) > 0 {
        client.AddRoots(roots...)
    }
}

func rootsEqual(a, b *mcp.Root) bool {
    if a == nil || b == nil {
        return a == b
    }
    if a.URI != b.URI {
        return false
    }
    // Use JSON to be resilient to optional fields differences across SDK versions.
    aj, _ := json.Marshal(a)
    bj, _ := json.Marshal(b)
    return string(aj) == string(bj)
}

func (g *Gateway) mountHandler(handler http.Handler) http.Handler {
	path := g.opts.Path
	if path == "" {
		return handler
	}
	mux := http.NewServeMux()
	g.mountPaths(mux, handler)
	return mux
}

func (g *Gateway) mountPaths(mux *http.ServeMux, handler http.Handler) {
	path := g.opts.Path
	if path == "" {
		mux.Handle("/", handler)
		return
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	mux.Handle(path, handler)
	if !strings.HasSuffix(path, "/") {
		mux.Handle(path+"/", handler)
	}
}

func (g *Gateway) syncContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if g.opts.SyncTimeout <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, g.opts.SyncTimeout)
}

func (g *Gateway) logError(msg string, err error, args ...any) {
	if err == nil {
		return
	}
	attrs := append([]any{"error", err}, args...)
	g.opts.Logger.Error(msg, attrs...)
}

func bindSession(ctx context.Context, session *mcp.ServerSession) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if session == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionContextKey{}, session)
}

func sessionFromContext(ctx context.Context) *mcp.ServerSession {
	if ctx == nil {
		return nil
	}
	if session, ok := ctx.Value(sessionContextKey{}).(*mcp.ServerSession); ok {
		return session
	}
	return nil
}

type sessionContextKey struct{}

func (g *Gateway) trackProgressForParams(serverID string, sink progressSink, params progressCarrier) func() {
	if g.progress == nil {
		return func() {}
	}
	return g.progress.track(serverID, sink, params)
}

func (g *Gateway) lookupProgressRecipient(serverID string, token any) progressSink {
	if g.progress == nil {
		return nil
	}
	return g.progress.lookup(serverID, token)
}

func cloneMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	return maps.Clone(meta)
}

func newOAuthProtectedResourceHandler(options Options) http.Handler {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodHead {
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":                 options.ResourceURL,
				"authorization_servers":    []string{options.AuthorizationServer},
				"scopes_supported":         []string{"openid", "profile", "email", "offline_access"},
				"bearer_methods_supported": []string{"header"},
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	return cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{http.MethodGet, http.MethodHead, http.MethodOptions},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           600,
	}).Handler(handler)
}
