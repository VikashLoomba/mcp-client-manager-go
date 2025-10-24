package mcpgateway

import (
	"maps"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	metaKeyServerID   = "mcpgateway.server_id"
	metaKeyNativeName = "mcpgateway.native_name"
	metaKeyNativeURI  = "mcpgateway.native_uri"
)

type featureIndex struct {
	ns NamespaceStrategy

	mu sync.RWMutex

	tools           map[string]toolTarget
	serverTools     map[string][]string
	prompts         map[string]promptTarget
	serverPrompts   map[string][]string
	resources       map[string]resourceTarget
	serverResources map[string][]string
	resourceReverse map[string]string
	templates       map[string]resourceTemplateTarget
	serverTemplates map[string][]string
}

type toolTarget struct {
	GatewayName string
	ServerID    string
	NativeName  string
}

type promptTarget struct {
	GatewayName string
	ServerID    string
	NativeName  string
}

type resourceTarget struct {
	GatewayURI string
	ServerID   string
	NativeURI  string
}

type resourceTemplateTarget struct {
	GatewayURI string
	ServerID   string
	NativeURI  string
}

type toolRegistration struct {
	Tool   *mcp.Tool
	Target toolTarget
}

type promptRegistration struct {
	Prompt *mcp.Prompt
	Target promptTarget
}

type resourceRegistration struct {
	Resource *mcp.Resource
	Target   resourceTarget
}

type resourceTemplateRegistration struct {
	Template *mcp.ResourceTemplate
	Target   resourceTemplateTarget
}

func newFeatureIndex(ns NamespaceStrategy) *featureIndex {
	return &featureIndex{
		ns:              ns,
		tools:           make(map[string]toolTarget),
		serverTools:     make(map[string][]string),
		prompts:         make(map[string]promptTarget),
		serverPrompts:   make(map[string][]string),
		resources:       make(map[string]resourceTarget),
		serverResources: make(map[string][]string),
		resourceReverse: make(map[string]string),
		templates:       make(map[string]resourceTemplateTarget),
		serverTemplates: make(map[string][]string),
	}
}

func (f *featureIndex) UpdateTools(serverID string, upstream []*mcp.Tool) (removed []string, added []toolRegistration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	removed = f.removeToolsLocked(serverID)
	added = make([]toolRegistration, 0, len(upstream))
	names := make([]string, 0, len(upstream))
	for _, tool := range upstream {
		if tool == nil {
			continue
		}
		gatewayName := f.ns.ToolName(serverID, tool.Name)
		clone := cloneTool(tool, gatewayName, serverID)
		target := toolTarget{GatewayName: gatewayName, ServerID: serverID, NativeName: tool.Name}
		f.tools[gatewayName] = target
		added = append(added, toolRegistration{Tool: clone, Target: target})
		names = append(names, gatewayName)
	}
	f.serverTools[serverID] = names
	return removed, added
}

func (f *featureIndex) UpdatePrompts(serverID string, upstream []*mcp.Prompt) (removed []string, added []promptRegistration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	removed = f.removePromptsLocked(serverID)
	added = make([]promptRegistration, 0, len(upstream))
	var names []string
	for _, prompt := range upstream {
		if prompt == nil {
			continue
		}
		gatewayName := f.ns.PromptName(serverID, prompt.Name)
		clone := clonePrompt(prompt, gatewayName, serverID)
		target := promptTarget{GatewayName: gatewayName, ServerID: serverID, NativeName: prompt.Name}
		f.prompts[gatewayName] = target
		added = append(added, promptRegistration{Prompt: clone, Target: target})
		names = append(names, gatewayName)
	}
	f.serverPrompts[serverID] = names
	return removed, added
}

func (f *featureIndex) UpdateResources(serverID string, upstream []*mcp.Resource) (removed []string, added []resourceRegistration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	removed = f.removeResourcesLocked(serverID)
	added = make([]resourceRegistration, 0, len(upstream))
	var names []string
	for _, resource := range upstream {
		if resource == nil {
			continue
		}
		gatewayURI := f.ns.ResourceURI(serverID, resource.URI)
		clone := cloneResource(resource, gatewayURI, serverID)
		target := resourceTarget{GatewayURI: gatewayURI, ServerID: serverID, NativeURI: resource.URI}
		f.resources[gatewayURI] = target
		f.resourceReverse[f.resourceKey(serverID, resource.URI)] = gatewayURI
		added = append(added, resourceRegistration{Resource: clone, Target: target})
		names = append(names, gatewayURI)
	}
	f.serverResources[serverID] = names
	return removed, added
}

func (f *featureIndex) UpdateResourceTemplates(serverID string, upstream []*mcp.ResourceTemplate) (removed []string, added []resourceTemplateRegistration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	removed = f.removeTemplatesLocked(serverID)
	added = make([]resourceTemplateRegistration, 0, len(upstream))
	var names []string
	for _, tpl := range upstream {
		if tpl == nil {
			continue
		}
		gatewayURI := f.ns.ResourceTemplateURI(serverID, tpl.URITemplate)
		clone := cloneResourceTemplate(tpl, gatewayURI, serverID)
		target := resourceTemplateTarget{GatewayURI: gatewayURI, ServerID: serverID, NativeURI: tpl.URITemplate}
		f.templates[gatewayURI] = target
		added = append(added, resourceTemplateRegistration{Template: clone, Target: target})
		names = append(names, gatewayURI)
	}
	f.serverTemplates[serverID] = names
	return removed, added
}

func (f *featureIndex) ToolTarget(name string) (toolTarget, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	t, ok := f.tools[name]
	return t, ok
}

func (f *featureIndex) PromptTarget(name string) (promptTarget, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	p, ok := f.prompts[name]
	return p, ok
}

func (f *featureIndex) ResourceTarget(uri string) (resourceTarget, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	r, ok := f.resources[uri]
	return r, ok
}

func (f *featureIndex) ResourceTemplateTarget(uri string) (resourceTemplateTarget, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	t, ok := f.templates[uri]
	return t, ok
}

func (f *featureIndex) removeToolsLocked(serverID string) []string {
	names := f.serverTools[serverID]
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		delete(f.tools, name)
	}
	delete(f.serverTools, serverID)
	return append([]string(nil), names...)
}

func (f *featureIndex) removePromptsLocked(serverID string) []string {
	names := f.serverPrompts[serverID]
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		delete(f.prompts, name)
	}
	delete(f.serverPrompts, serverID)
	return append([]string(nil), names...)
}

func (f *featureIndex) removeResourcesLocked(serverID string) []string {
	names := f.serverResources[serverID]
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		if target, ok := f.resources[name]; ok {
			delete(f.resourceReverse, f.resourceKey(target.ServerID, target.NativeURI))
		}
		delete(f.resources, name)
	}
	delete(f.serverResources, serverID)
	return append([]string(nil), names...)
}

func (f *featureIndex) ResourceTargetByNative(serverID, nativeURI string) (string, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	uri, ok := f.resourceReverse[f.resourceKey(serverID, nativeURI)]
	return uri, ok
}

func (f *featureIndex) resourceKey(serverID, nativeURI string) string {
	return serverID + "\x00" + nativeURI
}

func (f *featureIndex) removeTemplatesLocked(serverID string) []string {
	names := f.serverTemplates[serverID]
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		delete(f.templates, name)
	}
	delete(f.serverTemplates, serverID)
	return append([]string(nil), names...)
}

func cloneTool(tool *mcp.Tool, gatewayName, serverID string) *mcp.Tool {
	if tool == nil {
		return nil
	}
	clone := *tool
	clone.Name = gatewayName
	clone.Meta = withMeta(tool.Meta, map[string]any{
		metaKeyServerID:   serverID,
		metaKeyNativeName: tool.Name,
	})
	return &clone
}

func clonePrompt(prompt *mcp.Prompt, gatewayName, serverID string) *mcp.Prompt {
	if prompt == nil {
		return nil
	}
	clone := *prompt
	clone.Name = gatewayName
	clone.Meta = withMeta(prompt.Meta, map[string]any{
		metaKeyServerID:   serverID,
		metaKeyNativeName: prompt.Name,
	})
	return &clone
}

func cloneResource(resource *mcp.Resource, gatewayURI, serverID string) *mcp.Resource {
	if resource == nil {
		return nil
	}
	clone := *resource
	clone.URI = gatewayURI
	clone.Meta = withMeta(resource.Meta, map[string]any{
		metaKeyServerID:  serverID,
		metaKeyNativeURI: resource.URI,
	})
	return &clone
}

func cloneResourceTemplate(tpl *mcp.ResourceTemplate, gatewayURI, serverID string) *mcp.ResourceTemplate {
	if tpl == nil {
		return nil
	}
	clone := *tpl
	clone.URITemplate = gatewayURI
	clone.Meta = withMeta(tpl.Meta, map[string]any{
		metaKeyServerID:  serverID,
		metaKeyNativeURI: tpl.URITemplate,
	})
	return &clone
}

func withMeta(base map[string]any, extras map[string]any) map[string]any {
	out := maps.Clone(base)
	if out == nil {
		out = make(map[string]any)
	}
	for k, v := range extras {
		out[k] = v
	}
	return out
}
