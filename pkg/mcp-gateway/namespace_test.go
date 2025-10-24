package mcpgateway

import "testing"

func TestServerPrefixNamespaceResourceRoundTrip(t *testing.T) {
	ns := ServerPrefixNamespace{}
	gateway := ns.ResourceTemplateURI("alpha", "file://{path}")
	if gateway == "" {
		t.Fatalf("gateway uri empty")
	}
	native, ok := ns.NativeResourceTemplateURI("alpha", gateway)
	if !ok {
		t.Fatalf("expected decode to succeed")
	}
	if native != "file://{path}" {
		t.Fatalf("unexpected native value: %s", native)
	}
}

func TestServerPrefixNamespaceResourceDecodeMismatch(t *testing.T) {
	ns := ServerPrefixNamespace{}
	if _, ok := ns.NativeResourceURI("alpha", ns.ResourceURI("bravo", "file://foo")); ok {
		t.Fatalf("decode should fail when server ids differ")
	}
}
