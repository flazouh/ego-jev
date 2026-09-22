// Package typesafe calls the TypeSafe System One API.
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/flazouh/ego-jev/internal/policy"
)

const DefaultEndpoint = "https://api.typesafe.ai/v1/systemone"

type Client struct {
	Endpoint string
	APIKey   string
	HTTP     *http.Client
	// Attempts is the total number of tries for 429, 503, and 529 responses.
	Attempts int
	Backoff  time.Duration
}

func New(apiKey string) *Client {
	return &Client{Endpoint: DefaultEndpoint, APIKey: apiKey, HTTP: &http.Client{Timeout: 20 * time.Second}, Attempts: 3, Backoff: 400 * time.Millisecond}
}

// StatusError is a non-2xx answer from the API.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("typesafe returned HTTP %d: %s", e.Status, e.Body)
}

func retryable(status int) bool { return status == 429 || status == 503 || status == 529 }

func (c *Client) Choose(ctx context.Context, req policy.Request) (policy.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return policy.Response{}, err
	}
	for attempt := 1; ; attempt++ {
		resp, err := c.post(ctx, body)
		var se *StatusError
		if err == nil || attempt >= c.Attempts || !errors.As(err, &se) || !retryable(se.Status) {
			return resp, err
		}
		select {
		case <-ctx.Done():
			return policy.Response{}, ctx.Err()
		case <-time.After(c.Backoff << (attempt - 1)):
		}
	}
}

func (c *Client) post(ctx context.Context, body []byte) (policy.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return policy.Response{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(httpReq)
	if err != nil {
		return policy.Response{}, fmt.Errorf("typesafe request failed: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return policy.Response{}, err
	}
	if res.StatusCode/100 != 2 {
		return policy.Response{}, &StatusError{Status: res.StatusCode, Body: truncate(string(raw), 300)}
	}
	var out policy.Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return policy.Response{}, fmt.Errorf("typesafe returned unreadable JSON: %w", err)
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
