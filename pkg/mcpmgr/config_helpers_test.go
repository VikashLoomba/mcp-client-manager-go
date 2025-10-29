package mcpmgr

import (
    "reflect"
    "testing"
    "time"
)

func TestConfigHelpersDirect(t *testing.T) {
    t.Parallel()

    stdio := &StdioServerConfig{
        BaseServerConfig: BaseServerConfig{Timeout: 5 * time.Second, Version: "1.2.3"},
        Command:          "npx",
        Args:             []string{"@modelcontextprotocol/server-everything"},
        Env:              map[string]string{"A": "B"},
    }
    http := &HTTPServerConfig{
        BaseServerConfig: BaseServerConfig{Timeout: 10 * time.Second, Version: "2.0.0"},
        Endpoint:         "https://example",
        MaxRetries:       3,
        SessionID:        "sess",
    }

    if !IsStdio(stdio) || IsHTTP(stdio) {
        t.Fatalf("IsStdio/IsHTTP mismatch for stdio")
    }
    if !IsHTTP(http) || IsStdio(http) {
        t.Fatalf("IsHTTP/IsStdio mismatch for http")
    }

    if TransportOf(stdio) != TransportStdio {
        t.Fatalf("TransportOf(stdio) = %q", TransportOf(stdio))
    }
    if TransportOf(http) != TransportHTTP {
        t.Fatalf("TransportOf(http) = %q", TransportOf(http))
    }
    if TransportOf(nil) != "" {
        t.Fatalf("TransportOf(nil) should be empty")
    }

    if c, ok := AsStdio(stdio); !ok || c.Command != "npx" {
        t.Fatalf("AsStdio failed to narrow stdio: ok=%v cfg=%#v", ok, c)
    }
    if c, ok := AsHTTP(http); !ok || c.Endpoint != "https://example" {
        t.Fatalf("AsHTTP failed to narrow http: ok=%v cfg=%#v", ok, c)
    }
    if c, ok := AsStdio(http); ok || c != nil {
        t.Fatalf("AsStdio(http) should not narrow: ok=%v cfg=%#v", ok, c)
    }
    if c, ok := AsHTTP(stdio); ok || c != nil {
        t.Fatalf("AsHTTP(stdio) should not narrow: ok=%v cfg=%#v", ok, c)
    }
}

func TestConfigHelpersWithSummaries(t *testing.T) {
    t.Parallel()

    stdioID := "s-stdio"
    httpID := "s-http"
    cfg := map[string]ServerConfig{
        stdioID: &StdioServerConfig{
            BaseServerConfig: BaseServerConfig{Timeout: 7 * time.Second},
            Command:          "npx",
            Args:             []string{"@modelcontextprotocol/server-everything"},
        },
        httpID: &HTTPServerConfig{
            BaseServerConfig: BaseServerConfig{Timeout: 9 * time.Second},
            Endpoint:         "https://gitmcp.io/modelcontextprotocol/go-sdk",
        },
    }

    m := NewManager(cfg, &ManagerOptions{DefaultClientName: "helpers-test"})
    sums := m.GetServerSummaries()
    if len(sums) != 2 {
        t.Fatalf("expected 2 summaries, got %d", len(sums))
    }

    seen := map[ConfigTransport]bool{}
    for _, s := range sums {
        switch TransportOf(s.Config) {
        case TransportStdio:
            seen[TransportStdio] = true
            c, ok := AsStdio(s.Config)
            if !ok || c == nil || c.Command != "npx" {
                t.Fatalf("narrowed stdio invalid: ok=%v cfg=%#v", ok, c)
            }
        case TransportHTTP:
            seen[TransportHTTP] = true
            c, ok := AsHTTP(s.Config)
            if !ok || c == nil || c.Endpoint == "" {
                t.Fatalf("narrowed http invalid: ok=%v cfg=%#v", ok, c)
            }
        default:
            t.Fatalf("unknown transport for %s: %T", s.ID, s.Config)
        }
    }
    if !reflect.DeepEqual(seen, map[ConfigTransport]bool{TransportStdio: true, TransportHTTP: true}) {
        t.Fatalf("seen transports mismatch: %#v", seen)
    }
}

