package mcpgateway

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vikashloomba/mcp-client-manager-go/pkg/mcpmgr"
)

func TestEnsureProgressToken(t *testing.T) {
	g := newTestGateway()
	params := &mcp.CallToolParams{Name: "echo"}

	token, ok := g.progress.ensureToken("srv", params)
	if !ok {
		t.Fatalf("ensureToken returned false")
	}
	if token == nil {
		t.Fatalf("expected generated token, got nil")
	}
	if got := params.GetProgressToken(); got != token {
		t.Fatalf("params progress token mismatch: got %v want %v", got, token)
	}

	params.SetMeta(map[string]any{})
	params.SetProgressToken("existing-token")
	if tok, ok := g.progress.ensureToken("srv", params); !ok || tok != "existing-token" {
		t.Fatalf("expected existing token to be preserved, got %v", tok)
	}
}

func TestEnsureProgressTokenNormalizesFloat(t *testing.T) {
	g := newTestGateway()
	params := &mcp.CallToolParams{Name: "echo"}
	params.SetMeta(map[string]any{"progressToken": 3.0})

	token, ok := g.progress.ensureToken("srv", params)
	if !ok || token != int64(3) {
		t.Fatalf("expected float token to normalize to int64 3, got %v (%T)", token, token)
	}
	if stored := params.GetProgressToken(); stored != int64(3) {
		t.Fatalf("stored token mismatch: got %v", stored)
	}
}

func TestTrackProgressLifecycle(t *testing.T) {
	g := newTestGateway()
	sink := &fakeProgressSink{}

	cleanup := g.progress.register("srv", "token-1", sink)
	cleanup2 := g.progress.register("srv", int64(42), sink)

	if got := g.progress.lookup("srv", "token-1"); got != sink {
		t.Fatalf("expected sink lookup for string token, got %v", got)
	}
	if got := g.progress.lookup("srv", int64(42)); got != sink {
		t.Fatalf("expected sink lookup for int token, got %v", got)
	}

	cleanup()
	waitForProgressRemoval(t, func() bool {
		return g.progress.lookup("srv", "token-1") == nil
	})

	cleanup2()
	waitForProgressRemoval(t, func() bool {
		return g.progress.lookup("srv", int64(42)) == nil
	})
}

func TestForwardProgressDispatches(t *testing.T) {
	g := newTestGateway()
	sink := &fakeProgressSink{}
	token := int64(3)
	g.progress.register("srv", token, sink)

	handler := g.forwardProgress("srv")
	params := &mcp.ProgressNotificationParams{ProgressToken: float64(3), Progress: 0.5, Total: 1}
	req := &mcp.ProgressNotificationClientRequest{Params: params}
	handler(context.Background(), mcpmgr.NotificationPayload{
		ServerID: "srv",
		Method:   mcpmgr.NotificationSchemaProgress,
		Request:  req,
	})

	if sink.calls != 1 {
		t.Fatalf("expected NotifyProgress to be called once, got %d", sink.calls)
	}
	if sink.lastParams != params {
		t.Fatalf("expected params to match, got %+v", sink.lastParams)
	}
}

func newTestGateway() *Gateway {
	opts := (&Options{}).withDefaults()
	return &Gateway{
		opts:     opts,
		progress: newProgressTracker(slog.Default()),
	}
}

type fakeProgressSink struct {
	calls      int
	lastParams *mcp.ProgressNotificationParams
}

func (f *fakeProgressSink) NotifyProgress(ctx context.Context, params *mcp.ProgressNotificationParams) error {
	f.calls++
	f.lastParams = params
	return nil
}

func waitForProgressRemoval(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * progressCleanupGrace)
	if progressCleanupGrace <= 0 {
		deadline = time.Now().Add(100 * time.Millisecond)
	}
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !condition() {
		t.Fatalf("condition not met before timeout")
	}
}
