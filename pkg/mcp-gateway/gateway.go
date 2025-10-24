package mcpgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

// Gateway exposes a Streamable MCP server that fronts every server managed by
// mcpmgr under a single HTTP endpoint.
type Gateway struct {
	manager *mcpmgr.Manager
	opts    Options

	features *featureIndex

	server        *mcp.Server
	streamHandler *mcp.StreamableHTTPHandler
	httpHandler   http.Handler

	serverMu     sync.Mutex
	httpServerMu sync.Mutex
	httpServer   *http.Server

	registerMu      sync.Mutex
	registeredSrvID map[string]struct{}
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
	}

	g.server = mcp.NewServer(options.Implementation, &mcp.ServerOptions{
		HasTools:           true,
		HasPrompts:         true,
		HasResources:       true,
		SubscribeHandler:   g.handleSubscribe,
		UnsubscribeHandler: g.handleUnsubscribe,
	})
	g.streamHandler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return g.server
	}, &options.Streamable)
	g.httpHandler = g.mountHandler()

	mgr.SetElicitationCallback(g.forwardElicitation)

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

// ListenAndServe runs an HTTP server until the provided context is cancelled or
// the server stops.
func (g *Gateway) ListenAndServe(ctx context.Context) error {
	g.httpServerMu.Lock()
	if g.httpServer != nil {
		serv := g.httpServer
		g.httpServerMu.Unlock()
		return fmt.Errorf("mcpgateway: server already running on %s", serv.Addr)
	}
	srv := &http.Server{Addr: g.opts.Addr, Handler: g.Handler()}
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
	return g.SyncServer(ctx, serverID)
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
}

func (g *Gateway) syncAndLog(kind, serverID string, err error) {
	if err != nil {
		g.logError("sync "+kind, err, "server", serverID)
	}
}

func (g *Gateway) makeToolHandler(target toolTarget) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		callCtx := bindSession(ctx, req.Session)
		args := any(nil)
		if req.Params != nil {
			args = req.Params.Arguments
		}
		return g.manager.ExecuteTool(callCtx, target.ServerID, target.NativeName, args)
	}
}

func (g *Gateway) makePromptHandler(target promptTarget) mcp.PromptHandler {
	return func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		callCtx := bindSession(ctx, req.Session)
		params := &mcp.GetPromptParams{Name: target.NativeName}
		if req.Params != nil {
			params.Meta = req.Params.Meta
			if len(req.Params.Arguments) > 0 {
				params.Arguments = req.Params.Arguments
			}
		}
		return g.manager.GetPrompt(callCtx, target.ServerID, params)
	}
}

func (g *Gateway) makeResourceHandler(target resourceTarget) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		callCtx := bindSession(ctx, req.Session)
		params := &mcp.ReadResourceParams{URI: target.NativeURI}
		if req.Params != nil {
			params.Meta = req.Params.Meta
		}
		return g.manager.ReadResource(callCtx, target.ServerID, params)
	}
}

func (g *Gateway) makeResourceTemplateHandler(target resourceTemplateTarget) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		callCtx := bindSession(ctx, req.Session)
		native := target.NativeURI
		if req != nil && req.Params != nil {
			if candidate, ok := g.opts.Namespace.NativeResourceTemplateURI(target.ServerID, req.Params.URI); ok {
				native = candidate
			}
		}
		params := &mcp.ReadResourceParams{URI: native}
		if req != nil && req.Params != nil {
			params.Meta = req.Params.Meta
		}
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

func (g *Gateway) mountHandler() http.Handler {
	path := g.opts.Path
	if path == "" {
		return g.streamHandler
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	mux := http.NewServeMux()
	mux.Handle(path, g.streamHandler)
	if !strings.HasSuffix(path, "/") {
		mux.Handle(path+"/", g.streamHandler)
	}
	return mux
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
