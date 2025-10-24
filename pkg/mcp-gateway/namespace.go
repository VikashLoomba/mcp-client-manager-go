package mcpgateway

import (
	"fmt"
	"net/url"
	"strings"
)

// NamespaceStrategy generates the downstream identifiers for upstream MCP
// servers. Implementations must be deterministic and collision-free for a given
// serverID/name pair.
type NamespaceStrategy interface {
	ToolName(serverID, toolName string) string
	PromptName(serverID, promptName string) string
	ResourceURI(serverID, resourceURI string) string
	ResourceTemplateURI(serverID, templateURI string) string
	NativeResourceURI(serverID, gatewayURI string) (string, bool)
	NativeResourceTemplateURI(serverID, gatewayURI string) (string, bool)
}

// ServerPrefixNamespace prefixes every identifier with the originating server
// ID, separating fields with a configurable delimiter (defaults to "__" to stay
// within the MCP spec's character guidance).
type ServerPrefixNamespace struct {
	Separator string
}

func (s ServerPrefixNamespace) separator() string {
	if s.Separator == "" {
		return "__"
	}
	return s.Separator
}

func (s ServerPrefixNamespace) ToolName(serverID, toolName string) string {
	return s.decorate(serverID, toolName)
}

func (s ServerPrefixNamespace) PromptName(serverID, promptName string) string {
	return s.decorate(serverID, promptName)
}

func (s ServerPrefixNamespace) ResourceURI(serverID, resourceURI string) string {
	return s.resource("resources", serverID, resourceURI)
}

func (s ServerPrefixNamespace) ResourceTemplateURI(serverID, templateURI string) string {
	return s.resource("templates", serverID, templateURI)
}

func (s ServerPrefixNamespace) NativeResourceURI(serverID, gatewayURI string) (string, bool) {
	return s.decodeResource("resources", serverID, gatewayURI)
}

func (s ServerPrefixNamespace) NativeResourceTemplateURI(serverID, gatewayURI string) (string, bool) {
	return s.decodeResource("templates", serverID, gatewayURI)
}

func (s ServerPrefixNamespace) decorate(serverID, value string) string {
	return fmt.Sprintf("%s%s%s", serverID, s.separator(), value)
}

func (s ServerPrefixNamespace) resource(category, serverID, raw string) string {
	return fmt.Sprintf("mcpgateway+%s/%s::%s", url.PathEscape(serverID), strings.TrimPrefix(category, "/"), raw)
}

func (s ServerPrefixNamespace) decodeResource(category, serverID, gateway string) (string, bool) {
	prefix := fmt.Sprintf("mcpgateway+%s/%s::", url.PathEscape(serverID), strings.TrimPrefix(category, "/"))
	if !strings.HasPrefix(gateway, prefix) {
		return "", false
	}
	return strings.TrimPrefix(gateway, prefix), true
}
