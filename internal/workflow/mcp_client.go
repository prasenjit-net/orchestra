package workflow

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// MCPTool is an MCP tool descriptor, compatible with OpenAI's function schema.
type MCPTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

type sseEvent struct {
	event string
	data  string
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpSession struct {
	postURL string
	headers map[string]string
	client  *http.Client
	cancel  context.CancelFunc
	mu      sync.Mutex
	pending map[int64]chan jsonRPCResponse
	nextID  atomic.Int64
}

// ConnectMCPSession establishes an SSE connection and performs the initialize
// handshake, but does NOT call tools/list. Use this at runtime when the tool
// list is already known from the stored server record.
func ConnectMCPSession(ctx context.Context, httpClient *http.Client, serverURL string, headers map[string]string) (*mcpSession, error) {
	sess, _, err := connectMCP(ctx, httpClient, serverURL, headers, false)
	return sess, err
}

// ConnectMCPServer establishes an SSE connection to the MCP server, performs the
// initialize handshake, and returns the session along with the discovered tool list.
func ConnectMCPServer(ctx context.Context, httpClient *http.Client, serverURL string, headers map[string]string) (*mcpSession, []MCPTool, error) {
	return connectMCP(ctx, httpClient, serverURL, headers, true)
}

func connectMCP(ctx context.Context, httpClient *http.Client, serverURL string, headers map[string]string, discoverTools bool) (*mcpSession, []MCPTool, error) {
	sseCtx, cancel := context.WithCancel(ctx)

	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, serverURL, nil)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("build sse request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("connect sse: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, nil, fmt.Errorf("sse connect returned %d", resp.StatusCode)
	}

	eventCh := make(chan sseEvent, 32)
	go func() {
		defer resp.Body.Close()
		defer close(eventCh)
		scanner := bufio.NewScanner(resp.Body)
		var ev, data strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event:"):
				ev.Reset()
				ev.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "event:")))
			case strings.HasPrefix(line, "data:"):
				data.Reset()
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			case line == "":
				if ev.Len() > 0 || data.Len() > 0 {
					select {
					case eventCh <- sseEvent{event: ev.String(), data: data.String()}:
					case <-sseCtx.Done():
						return
					}
					ev.Reset()
					data.Reset()
				}
			}
		}
	}()

	sess := &mcpSession{
		postURL: "",
		headers: headers,
		client:  httpClient,
		cancel:  cancel,
		pending: make(map[int64]chan jsonRPCResponse),
	}
	sess.nextID.Store(1)

	// Start dispatcher goroutine.
	go func() {
		for ev := range eventCh {
			if ev.event == "endpoint" {
				sess.mu.Lock()
				if sess.postURL == "" {
					sess.postURL = ev.data
				}
				sess.mu.Unlock()
				continue
			}
			if ev.event == "message" || ev.event == "" {
				var rpc jsonRPCResponse
				if err := json.Unmarshal([]byte(ev.data), &rpc); err != nil {
					continue
				}
				if rpc.ID == nil {
					continue
				}
				sess.mu.Lock()
				ch, ok := sess.pending[*rpc.ID]
				if ok {
					delete(sess.pending, *rpc.ID)
				}
				sess.mu.Unlock()
				if ok {
					select {
					case ch <- rpc:
					default:
					}
				}
			}
		}
	}()

	// Wait for endpoint event.
	if err := waitForEndpoint(ctx, sess); err != nil {
		cancel()
		return nil, nil, err
	}

	// Initialize handshake.
	id := sess.allocID()
	initResult, err := sess.sendRequest(ctx, id, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "orchestra", "version": "1.0"},
	})
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("mcp initialize: %w", err)
	}
	_ = initResult

	// Send initialized notification (no response).
	_ = sess.notify(ctx, "notifications/initialized", nil)

	if !discoverTools {
		return sess, nil, nil
	}

	// Discover tools.
	toolsID := sess.allocID()
	toolsResult, err := sess.sendRequest(ctx, toolsID, "tools/list", nil)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("mcp tools/list: %w", err)
	}

	var toolsPayload struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(toolsResult, &toolsPayload); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("decode tools/list result: %w", err)
	}

	return sess, toolsPayload.Tools, nil
}

// CallTool invokes a named tool and returns its text output.
func (s *mcpSession) CallTool(ctx context.Context, toolName string, args map[string]any) (string, error) {
	id := s.allocID()
	result, err := s.sendRequest(ctx, id, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}

	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return string(result), nil
	}
	if payload.IsError {
		var parts []string
		for _, c := range payload.Content {
			if c.Type == "text" {
				parts = append(parts, c.Text)
			}
		}
		return "", fmt.Errorf("mcp tool error: %s", strings.Join(parts, "; "))
	}

	var parts []string
	for _, c := range payload.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// Close terminates the SSE connection.
func (s *mcpSession) Close() {
	s.cancel()
}

func (s *mcpSession) allocID() int64 {
	return s.nextID.Add(1)
}

func (s *mcpSession) sendRequest(ctx context.Context, id int64, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	postURL := s.postURL
	ch := make(chan jsonRPCResponse, 1)
	s.pending[id] = ch
	s.mu.Unlock()

	if postURL == "" {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("mcp post URL not yet received")
	}

	body, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("encode rpc request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("build rpc request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	postResp, err := s.client.Do(req)
	if err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("post rpc: %w", err)
	}
	io.Copy(io.Discard, postResp.Body) //nolint:errcheck
	postResp.Body.Close()

	select {
	case rpc := <-ch:
		if rpc.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", rpc.Error.Code, rpc.Error.Message)
		}
		return rpc.Result, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *mcpSession) notify(ctx context.Context, method string, params any) error {
	s.mu.Lock()
	postURL := s.postURL
	s.mu.Unlock()

	body, _ := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", Method: method, Params: params})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body) //nolint:errcheck
	resp.Body.Close()
	return nil
}

func waitForEndpoint(ctx context.Context, sess *mcpSession) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		sess.mu.Lock()
		url := sess.postURL
		sess.mu.Unlock()
		if url != "" {
			return nil
		}
		// Small busy-wait — the endpoint event arrives almost immediately.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}
