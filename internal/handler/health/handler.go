package health

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/disillusioned-labs/expense/internal/handler"

	"go.opentelemetry.io/otel/trace"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

type Handler struct {
	required map[string]Pinger
	optional map[string]Pinger
	draining atomic.Bool
}

func NewHandler(required, optional map[string]Pinger) *Handler {
	return &Handler{required: required, optional: optional}
}

func (h *Handler) BeginDrain() { h.draining.Store(true) }

func (h *Handler) Liveness(w http.ResponseWriter, _ *http.Request) {
	handler.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

var probeParent = trace.NewSpanContext(trace.SpanContextConfig{
	TraceID: trace.TraceID{0x70, 0x72, 0x6f, 0x62, 0x65, 1},
	SpanID:  trace.SpanID{0x70, 0x72, 0x6f, 0x62, 0x65, 1},
})

func unsampled(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(ctx, probeParent)
}

func (h *Handler) Readiness(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		handler.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "draining",
		})
		return
	}

	ctx, cancel := context.WithTimeout(unsampled(r.Context()), 3*time.Second)
	defer cancel()

	status := http.StatusOK
	checks := make(map[string]string, len(h.required)+len(h.optional))

	for name, dep := range h.required {
		if err := dep.Ping(ctx); err != nil {
			checks[name] = "down: " + err.Error()
			status = http.StatusServiceUnavailable
		} else {
			checks[name] = "up"
		}
	}
	for name, dep := range h.optional {
		if err := dep.Ping(ctx); err != nil {
			checks[name] = "degraded: " + err.Error()
		} else {
			checks[name] = "up"
		}
	}

	handler.WriteJSON(w, status, map[string]any{"checks": checks})
}
