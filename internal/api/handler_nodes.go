package api

import (
	"net/http"
	"sync"
	"time"
)

func (h *Handler) ListNodes(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	nodes, err := h.workflow.ListNodes(r.Context(), h.config.Node.Health.OfflineThreshold)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, nodes)
}

type nodeHealthResult struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	OK        bool   `json:"ok"`
	Status    int    `json:"status,omitempty"`
	LatencyMs int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
	CheckedAt string `json:"checkedAt"`
}

func (h *Handler) CheckNodeHealth(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	nodes, err := h.workflow.ListNodes(r.Context(), h.config.Node.Health.OfflineThreshold)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	results := make([]nodeHealthResult, len(nodes))
	client := &http.Client{Timeout: 5 * time.Second}
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func(i int, id, address string) {
			defer wg.Done()
			res := nodeHealthResult{ID: id, Address: address, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
			if address == "" {
				res.Error = "no address registered"
				results[i] = res
				return
			}
			start := time.Now()
			resp, err := client.Get(address + "/livez")
			res.LatencyMs = time.Since(start).Milliseconds()
			if err != nil {
				res.Error = err.Error()
			} else {
				resp.Body.Close()
				res.Status = resp.StatusCode
				res.OK = resp.StatusCode == http.StatusOK
			}
			results[i] = res
		}(i, n.ID, n.Address)
	}
	wg.Wait()

	respondJSON(w, http.StatusOK, results)
}
