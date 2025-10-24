package mcpgateway

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFeatureIndexUpdateTools(t *testing.T) {
	fi := newFeatureIndex(ServerPrefixNamespace{})
	tools := []*mcp.Tool{{Name: "echo"}}
	removed, added := fi.UpdateTools("alpha", tools)
	if len(removed) != 0 {
		t.Fatalf("unexpected removals: %v", removed)
	}
	if len(added) != 1 {
		t.Fatalf("expected single registration, got %d", len(added))
	}
	target := added[0].Target
	if target.ServerID != "alpha" || target.NativeName != "echo" {
		t.Fatalf("unexpected target %+v", target)
	}
	lookup, ok := fi.ToolTarget(target.GatewayName)
	if !ok {
		t.Fatalf("tool target missing")
	}
	if lookup.NativeName != "echo" {
		t.Fatalf("lookup mismatch: %+v", lookup)
	}
	meta := added[0].Tool.Meta
	if meta[metaKeyServerID] != "alpha" {
		t.Fatalf("meta missing server id: %+v", meta)
	}
}

func TestFeatureIndexResourceRoundTrip(t *testing.T) {
	fi := newFeatureIndex(ServerPrefixNamespace{})
	resources := []*mcp.Resource{{URI: "file://notes"}}
	_, added := fi.UpdateResources("bravo", resources)
	if len(added) != 1 {
		t.Fatalf("expected 1 resource registration")
	}
	gateway := added[0].Resource.URI
	if _, ok := fi.ResourceTarget(gateway); !ok {
		t.Fatalf("resource target missing")
	}
	if native, ok := fi.ResourceTargetByNative("bravo", "file://notes"); !ok || native != gateway {
		t.Fatalf("reverse lookup failed: %v %s", ok, native)
	}
}
