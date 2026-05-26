package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// NodeInfo holds the registration details written to the nodes table at startup.
type NodeInfo struct {
	ID            string
	Role          string // "controller" | "worker" | "all"
	Address       string // HTTP URI, e.g. http://10.0.1.5:8080
	Capabilities  []string
	MaxConcurrent int
	Version       string
	Hostname      string
}

// NodeStatus is a registered node row with a derived online/offline status.
type NodeStatus struct {
	ID            string    `json:"id"`
	Role          string    `json:"role"`
	Address       string    `json:"address"`
	Capabilities  []string  `json:"capabilities"`
	MaxConcurrent int       `json:"maxConcurrent"`
	Version       string    `json:"version"`
	Hostname      string    `json:"hostname"`
	LastSeenAt    time.Time `json:"lastSeenAt"`
	RegisteredAt  time.Time `json:"registeredAt"`
	Status        string    `json:"status"` // "online" | "offline" — derived, not stored
}

// RegisterNode upserts a row in the nodes table for this process.
func (s *Service) RegisterNode(ctx context.Context, info NodeInfo) error {
	caps, err := json.Marshal(info.Capabilities)
	if err != nil {
		return fmt.Errorf("marshal node capabilities: %w", err)
	}
	now := formatTime(time.Now())
	_, err = s.db.ExecContext(ctx, s.rebind(`
		INSERT INTO nodes (id, role, address, capabilities, max_concurrent, version, hostname, last_seen_at, registered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			role           = excluded.role,
			address        = excluded.address,
			capabilities   = excluded.capabilities,
			max_concurrent = excluded.max_concurrent,
			version        = excluded.version,
			hostname       = excluded.hostname,
			last_seen_at   = excluded.last_seen_at
	`),
		info.ID, info.Role, info.Address, string(caps),
		info.MaxConcurrent, info.Version, info.Hostname,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("register node: %w", err)
	}
	return nil
}

// DeregisterNode removes this node's row from the nodes table on graceful shutdown.
func (s *Service) DeregisterNode(ctx context.Context, nodeID string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM nodes WHERE id = ?`), nodeID)
	if err != nil {
		return fmt.Errorf("deregister node: %w", err)
	}
	return nil
}

// HeartbeatNode updates last_seen_at for this node and publishes a live event.
func (s *Service) HeartbeatNode(ctx context.Context, nodeID string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE nodes SET last_seen_at = ? WHERE id = ?`),
		formatTime(time.Now()), nodeID,
	)
	if err != nil {
		return fmt.Errorf("heartbeat node: %w", err)
	}
	s.emitLiveEvent("nodes.updated", "node", nodeID, nil)
	return nil
}

// ListNodes returns all registered nodes with a derived status field.
func (s *Service) ListNodes(ctx context.Context, offlineThreshold time.Duration) ([]NodeStatus, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, role, address, capabilities, max_concurrent, version, hostname, last_seen_at, registered_at
		FROM nodes
		ORDER BY registered_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	now := time.Now()
	var nodes []NodeStatus
	for rows.Next() {
		var n NodeStatus
		var capsJSON, lastSeenStr, registeredStr string
		if err := rows.Scan(
			&n.ID, &n.Role, &n.Address, &capsJSON,
			&n.MaxConcurrent, &n.Version, &n.Hostname,
			&lastSeenStr, &registeredStr,
		); err != nil {
			return nil, fmt.Errorf("scan node: %w", err)
		}
		_ = json.Unmarshal([]byte(capsJSON), &n.Capabilities)
		if n.Capabilities == nil {
			n.Capabilities = []string{}
		}
		n.LastSeenAt = mustParseTime(lastSeenStr)
		n.RegisteredAt = mustParseTime(registeredStr)
		if now.Sub(n.LastSeenAt) <= offlineThreshold {
			n.Status = "online"
		} else {
			n.Status = "offline"
		}
		nodes = append(nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	if nodes == nil {
		nodes = []NodeStatus{}
	}
	return nodes, nil
}

// ActivityNames returns the names of all registered activities on this service.
func (s *Service) ActivityNames() []string {
	names := make([]string, 0, len(s.activities))
	for name := range s.activities {
		names = append(names, name)
	}
	return names
}
