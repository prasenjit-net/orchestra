package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/prasenjit-net/orchestra/internal/livebus"
)

func (h *Handler) WorkflowStream(w http.ResponseWriter, r *http.Request) {
	if h.live == nil {
		writeError(w, http.StatusServiceUnavailable, "live bus unavailable")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: h.websocketOriginPatterns()})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	readCtx, readCancel := context.WithCancel(r.Context())
	defer readCancel()
	go func() {
		_ = conn.CloseRead(readCtx)
	}()

	events, unsubscribe := h.live.Subscribe()
	defer unsubscribe()

	if err := wsjson.Write(r.Context(), conn, livebus.NewEvent("connection.ready", "connection", "app-live-bus", map[string]any{
		"status": "connected",
	})); err != nil {
		return
	}

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			_ = conn.Close(websocket.StatusNormalClosure, "client disconnected")
			return
		case event, ok := <-events:
			if !ok {
				_ = conn.Close(websocket.StatusGoingAway, "workflow stream closed")
				return
			}
			if err := wsjson.Write(r.Context(), conn, event); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := wsjson.Write(r.Context(), conn, livebus.NewEvent("heartbeat", "connection", "", nil)); err != nil {
				return
			}
		}
	}
}

func (h *Handler) websocketOriginPatterns() []string {
	values := []string{h.config.App.URL}
	if h.config.App.Env == "development" {
		values = append(values, h.config.UI.DevProxyURL)
	}
	patterns := make([]string, 0, len(values))
	for _, value := range values {
		parsed, err := url.Parse(value)
		if err == nil && parsed.Host != "" {
			patterns = append(patterns, parsed.Host)
		}
	}
	return patterns
}
