package qmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Default timeouts. Smart queries can be slow the first time QMD loads its models.
const (
	DefaultHealthTimeout = 5 * time.Second
	DefaultQueryTimeout  = 120 * time.Second
)

var (
	// ErrUnavailable means the QMD server could not be reached (not running, wrong URL).
	ErrUnavailable = errors.New("QMD is not running")
	// ErrTimeout means QMD did not answer in time.
	ErrTimeout = errors.New("QMD did not answer in time")
	// ErrInvalidRequest means the query was rejected before it was sent.
	ErrInvalidRequest = errors.New("invalid query")
)

// StatusError is returned when QMD answers with a non-2xx status.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("QMD returned HTTP %d", e.Code)
	}
	return fmt.Sprintf("QMD returned HTTP %d: %s", e.Code, e.Message)
}

// Client talks to a running QMD HTTP server.
type Client struct {
	BaseURL       string
	HTTP          *http.Client
	HealthTimeout time.Duration
	QueryTimeout  time.Duration
}

// New returns a client for the QMD server at baseURL (e.g. http://localhost:8181).
func New(baseURL string) *Client {
	return &Client{
		BaseURL:       strings.TrimRight(baseURL, "/"),
		HTTP:          &http.Client{},
		HealthTimeout: DefaultHealthTimeout,
		QueryTimeout:  DefaultQueryTimeout,
	}
}

// Health calls GET /health.
func (c *Client) Health(ctx context.Context) (Health, error) {
	ctx, cancel := context.WithTimeout(ctx, c.HealthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return Health{}, err
	}
	var h Health
	if err := c.do(req, &h); err != nil {
		return Health{}, err
	}
	return h, nil
}

// Query calls POST /query and returns the results (possibly empty).
func (c *Client) Query(ctx context.Context, q QueryRequest) ([]Result, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(q)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.QueryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp QueryResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}
	if resp.Results == nil {
		resp.Results = []Result{}
	}
	return resp.Results, nil
}

// Validate checks the request against QMD's documented limits.
func (q QueryRequest) Validate() error {
	if len(q.Searches) == 0 || len(q.Searches) > MaxSearches {
		return fmt.Errorf("%w: need 1-%d searches, got %d", ErrInvalidRequest, MaxSearches, len(q.Searches))
	}
	for _, s := range q.Searches {
		if strings.TrimSpace(s.Query) == "" {
			return fmt.Errorf("%w: empty query", ErrInvalidRequest)
		}
		switch s.Type {
		case Lex, Vec, HyDE:
		default:
			return fmt.Errorf("%w: unknown search type %q", ErrInvalidRequest, s.Type)
		}
	}
	if q.Limit < 0 {
		return fmt.Errorf("%w: negative limit", ErrInvalidRequest)
	}
	return nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ErrTimeout
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return &StatusError{Code: resp.StatusCode, Message: errorMessage(raw)}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ErrTimeout
		}
		return fmt.Errorf("decode QMD response: %w", err)
	}
	return nil
}

// errorMessage extracts {"error": "..."} or {"message": "..."} if present, else the raw text.
func errorMessage(raw []byte) string {
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &body) == nil {
		if body.Error != "" {
			return body.Error
		}
		if body.Message != "" {
			return body.Message
		}
	}
	return strings.TrimSpace(string(raw))
}
