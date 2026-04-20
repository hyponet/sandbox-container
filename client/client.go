package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const maxResponseBody = 64 * 1024 * 1024 // 64MB

const defaultAgentListCacheTTL = 5 * time.Minute

type agentListCacheEntry struct {
	result    *AgentSkillListResult
	expiresAt time.Time
}

// Client is an HTTP client for the sandbox-container API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client

	agentListTTL    time.Duration
	agentListMu     sync.Mutex
	agentListCache  map[string]*agentListCacheEntry
	agentListFlight singleflight.Group
}

// ClientOption configures optional Client behaviour.
type ClientOption func(*Client)

// WithAgentListCacheTTL overrides the default 5-minute TTL for SkillAgentList cache.
func WithAgentListCacheTTL(d time.Duration) ClientOption {
	return func(c *Client) { c.agentListTTL = d }
}

// NewClient creates a new Client pointing at the given base URL (e.g. "http://localhost:9090").
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		agentListTTL:   defaultAgentListCacheTTL,
		agentListCache: make(map[string]*agentListCacheEntry),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// agentListCacheKey builds a cache key from the request parameters.
func agentListCacheKey(agentID string, req *agentSkillRequest) string {
	ids := make([]string, len(req.SkillIDs))
	copy(ids, req.SkillIDs)
	sort.Strings(ids)

	var b strings.Builder
	b.WriteString(agentID)
	b.WriteByte('\x00')
	b.WriteString(strings.Join(ids, "\x00"))
	if req.Cleanup {
		b.WriteString("\x00cleanup")
	}
	if req.EnableAgentWorkspace {
		b.WriteString("\x00workspace")
	}
	return b.String()
}

// WithAPIKey sets the API key for authentication.
func (c *Client) WithAPIKey(key string) *Client {
	c.apiKey = key
	return c
}

// SetHTTPClient sets a custom http.Client.
func (c *Client) SetHTTPClient(hc *http.Client) {
	c.httpClient = hc
}

// doJSON sends a JSON request and unmarshals the APIResponse.Data into result.
func (c *Client) doJSON(method, path string, body interface{}, result interface{}) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	return c.handleResponse(resp, result)
}

// handleResponse reads and processes an HTTP response. For error status codes it
// returns *Error. For success it unmarshals: wrapped (apiResponse.Data) first,
// then falls back to direct unmarshal for endpoints that don't use the wrapper.
func (c *Client) handleResponse(resp *http.Response, result interface{}) error {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var apiResp apiResponse
		if json.Unmarshal(raw, &apiResp) == nil && apiResp.Message != nil {
			return &Error{StatusCode: resp.StatusCode, Message: *apiResp.Message}
		}
		return &Error{StatusCode: resp.StatusCode, Message: string(raw)}
	}

	if result != nil {
		// Try wrapped response first (most endpoints use apiResponse)
		var apiResp apiResponse
		if err := json.Unmarshal(raw, &apiResp); err == nil && apiResp.Success {
			dataBytes, err := json.Marshal(apiResp.Data)
			if err != nil {
				return fmt.Errorf("remarshal data: %w", err)
			}
			return json.Unmarshal(dataBytes, result)
		}
		// Fallback: direct unmarshal (e.g. GetSandboxContext)
		return json.Unmarshal(raw, result)
	}

	return nil
}

// post is a convenience wrapper for POST JSON requests.
func (c *Client) post(path string, body interface{}, result interface{}) error {
	return c.doJSON(http.MethodPost, path, body, result)
}

// get is a convenience wrapper for GET requests.
func (c *Client) get(path string, result interface{}) error {
	return c.doJSON(http.MethodGet, path, nil, result)
}

// evictExpiredAgentListCacheLocked removes expired entries from the cache.
// Must be called with agentListMu held.
func (c *Client) evictExpiredAgentListCacheLocked() {
	now := time.Now()
	for key, entry := range c.agentListCache {
		if now.After(entry.expiresAt) {
			delete(c.agentListCache, key)
		}
	}
}

// invalidateAgentListCache removes all cached SkillAgentList entries for the given agent.
func (c *Client) invalidateAgentListCache(agentID string) {
	prefix := agentID + "\x00"
	c.agentListMu.Lock()
	for key := range c.agentListCache {
		if strings.HasPrefix(key, prefix) {
			delete(c.agentListCache, key)
		}
	}
	c.agentListMu.Unlock()
}

// nullableString returns a pointer to s if non-empty, nil otherwise.
func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
