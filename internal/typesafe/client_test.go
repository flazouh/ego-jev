package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flazouh/ego-jev/internal/policy"
)

func server(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("test-key")
	c.Endpoint = srv.URL
	c.Backoff = time.Millisecond
	return c
}

func TestChooseSendsTheRequestWithTheKeyAndDecodesAnswers(t *testing.T) {
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		var req policy.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model != "jev-latest" {
			t.Errorf("decode: %v model=%q", err, req.Model)
		}
		w.Write([]byte(`{"model":"jev-1.13.0","answers":{"is_urgent":{"type":"noul","noul":0.94}}}`))
	})
	resp, err := c.Choose(context.Background(), policy.Request{Model: "jev-latest"})
	if err != nil {
		t.Fatal(err)
	}
	if n := resp.Answers["is_urgent"].Noul; n == nil || *n != 0.94 {
		t.Fatalf("answers = %+v", resp.Answers)
	}
}

func TestChooseRetriesOverloadThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(529)
			return
		}
		w.Write([]byte(`{"answers":{}}`))
	})
	if _, err := c.Choose(context.Background(), policy.Request{}); err != nil || calls.Load() != 3 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestChooseDoesNotRetryAuthErrors(t *testing.T) {
	var calls atomic.Int32
	c := server(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"detail":"bad key"}`, http.StatusUnauthorized)
	})
	_, err := c.Choose(context.Background(), policy.Request{})
	var se *StatusError
	if !errors.As(err, &se) || se.Status != 401 || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}
