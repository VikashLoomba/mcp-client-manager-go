package mcpgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"log/slog"
)

type progressSink interface {
	NotifyProgress(context.Context, *mcp.ProgressNotificationParams) error
}

type progressCarrier interface {
	mcp.Params
	GetProgressToken() any
	SetProgressToken(any)
}

type progressTracker struct {
	counter atomic.Uint64
	seq     atomic.Uint64

	mu       sync.RWMutex
	sessions map[string]progressRegistration

	logger       *slog.Logger
	cleanupGrace time.Duration
}

type progressRegistration struct {
	sink progressSink
	seq  uint64
}

const progressCleanupGrace = 250 * time.Millisecond

func newProgressTracker(logger *slog.Logger) *progressTracker {
	return &progressTracker{
		sessions:     make(map[string]progressRegistration),
		logger:       logger,
		cleanupGrace: progressCleanupGrace,
	}
}

func (pt *progressTracker) track(serverID string, sink progressSink, carrier progressCarrier) func() {
	if carrier == nil || sink == nil {
		return func() {}
	}
	token, ok := pt.ensureToken(serverID, carrier)
	if !ok || token == nil {
		return func() {}
	}
	return pt.register(serverID, token, sink)
}

func (pt *progressTracker) ensureToken(serverID string, carrier progressCarrier) (any, bool) {
	if carrier == nil {
		return nil, false
	}
	existing := carrier.GetProgressToken()
	if existing != nil {
		normalized, ok := normalizeProgressToken(existing)
		if !ok {
			pt.logWarn("progress token unsupported", serverID, existing)
			return nil, false
		}
		if normalized != existing {
			ensureProgressMeta(carrier)
			carrier.SetProgressToken(normalized)
		}
		return normalized, true
	}
	ensureProgressMeta(carrier)
	token := fmt.Sprintf("gw/%s/%d", serverID, pt.counter.Add(1))
	carrier.SetProgressToken(token)
	return token, true
}

func (pt *progressTracker) register(serverID string, token any, sink progressSink) func() {
	normalized, ok := normalizeProgressToken(token)
	if !ok {
		pt.logWarn("progress token unsupported", serverID, token)
		return func() {}
	}
	key, ok := progressMapKey(serverID, normalized)
	if !ok {
		return func() {}
	}
	seq := pt.seq.Add(1)
	pt.mu.Lock()
	pt.sessions[key] = progressRegistration{sink: sink, seq: seq}
	pt.mu.Unlock()
	return func() {
		pt.removeLater(key, sink, seq)
	}
}

func (pt *progressTracker) removeLater(key string, sink progressSink, seq uint64) {
	grace := pt.cleanupGrace
	if grace <= 0 {
		pt.removeIfMatch(key, sink, seq)
		return
	}
	time.AfterFunc(grace, func() {
		pt.removeIfMatch(key, sink, seq)
	})
}

func (pt *progressTracker) removeIfMatch(key string, sink progressSink, seq uint64) {
	pt.mu.Lock()
	if current, ok := pt.sessions[key]; ok && current.seq == seq && current.sink == sink {
		delete(pt.sessions, key)
	}
	pt.mu.Unlock()
}

func (pt *progressTracker) lookup(serverID string, token any) progressSink {
	normalized, ok := normalizeProgressToken(token)
	if !ok {
		pt.logWarn("progress token unsupported", serverID, token)
		return nil
	}
	key, ok := progressMapKey(serverID, normalized)
	if !ok {
		return nil
	}
	pt.mu.RLock()
	sink := pt.sessions[key].sink
	pt.mu.RUnlock()
	return sink
}

func (pt *progressTracker) logWarn(msg, serverID string, token any) {
	if pt.logger == nil {
		return
	}
	pt.logger.Warn(msg, "server", serverID, "token", token)
}

func progressMapKey(serverID string, token any) (string, bool) {
	switch v := token.(type) {
	case string:
		return serverID + "|s|" + v, true
	case int64:
		return fmt.Sprintf("%s|i|%d", serverID, v), true
	case int:
		return fmt.Sprintf("%s|i|%d", serverID, v), true
	case int32:
		return fmt.Sprintf("%s|i|%d", serverID, v), true
	default:
		return "", false
	}
}

func normalizeProgressToken(token any) (any, bool) {
	switch v := token.(type) {
	case nil:
		return nil, false
	case string:
		return v, true
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", false
		}
		if math.Trunc(v) == v {
			return int64(v), true
		}
		return fmt.Sprintf("%g", v), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i, true
		}
		if f, err := v.Float64(); err == nil {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return "", false
			}
			if math.Trunc(f) == f {
				return int64(f), true
			}
			return fmt.Sprintf("%g", f), true
		}
		return v.String(), true
	default:
		return fmt.Sprintf("%v", v), true
	}
}

func ensureProgressMeta(params progressCarrier) {
	if params.GetMeta() == nil {
		params.SetMeta(map[string]any{})
	}
}
