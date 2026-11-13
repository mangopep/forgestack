// Package integration exercises the fully assembled application handler
// (internal/platform/router.New()) with real HTTP requests — the same
// scenarios the original single-file internal/api/api_test.go covered,
// preserved here so the refactor is proven behavior-equivalent.
package integration

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/mangopep/forgestack/internal/platform/router"
)

func TestHealth(t *testing.T) {
	r := httptest.NewRecorder()
	router.New().ServeHTTP(r, httptest.NewRequest("GET", "/healthz", nil))
	if r.Code != 200 {
		t.Fatalf("want 200, got %d", r.Code)
	}
	if r.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatal("missing JSON content type")
	}
	var body struct {
		Status, Service, Version string
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Service == "" || body.Version == "" {
		t.Errorf("expected service metadata contract fields (docs/platform/CONTRACTS.md §2) to be non-empty, got %+v", body)
	}
}

// TestCorrelationIDIsWiredIntoTheRealRouter proves the end-to-end behavior,
// not just the middleware in isolation (see
// internal/platform/middleware/middleware_test.go for that) — this is the
// test that would actually catch a mistake in router.New()'s middleware
// chain ordering.
func TestCorrelationIDIsWiredIntoTheRealRouter(t *testing.T) {
	handler := router.New()

	r := httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("GET", "/healthz", nil))
	if r.Header().Get("X-Request-Id") == "" {
		t.Error("expected the real assembled router to echo a generated X-Request-Id")
	}

	req := httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("X-Request-Id", "integration-test-trace")
	r = httptest.NewRecorder()
	handler.ServeHTTP(r, req)
	if got := r.Header().Get("X-Request-Id"); got != "integration-test-trace" {
		t.Errorf("expected the real router to reuse an inbound X-Request-Id, got %q", got)
	}
}

func TestReadyAndMetricsDemo(t *testing.T) {
	h := router.New()
	for _, path := range []string{"/readyz", "/metrics-demo"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 {
			t.Fatalf("%s: want 200, got %d", path, r.Code)
		}
	}
}

func TestPostLifecycle(t *testing.T) {
	handler := router.New()
	data := []byte(`{"author":"Nia","text":"hello Alex"}`)
	r := httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("POST", "/api/v1/posts", bytes.NewReader(data)))
	if r.Code != 201 {
		t.Fatalf("want 201 got %d: %s", r.Code, r.Body.String())
	}
	r = httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/posts?limit=1", nil))
	var out struct {
		Posts []struct {
			Author string `json:"author"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Posts) != 1 || out.Posts[0].Author != "Nia" {
		t.Fatalf("unexpected posts %+v", out.Posts)
	}
}

func TestValidation(t *testing.T) {
	for _, tt := range []struct{ method, path, body string }{
		{"POST", "/api/v1/posts", `{"author":"","text":"ok"}`},
		{"POST", "/api/v1/posts", `{"author":"a","text":"x","admin":true}`},
		{"GET", "/api/v1/posts?limit=999", ""},
	} {
		r := httptest.NewRecorder()
		router.New().ServeHTTP(r, httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body)))
		if r.Code != 400 {
			t.Fatalf("%s %s: want 400 got %d", tt.method, tt.path, r.Code)
		}
	}
}

func TestConcurrentRequests(t *testing.T) {
	h := router.New()
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRecorder()
			h.ServeHTTP(r, httptest.NewRequest("POST", "/api/v1/posts", bytes.NewBufferString(`{"author":"test","text":"hello"}`)))
			if r.Code != 201 {
				t.Errorf("status %d", r.Code)
			}
		}()
	}
	wg.Wait()
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/posts?limit=100", nil))
	var output struct {
		Posts []struct {
			Author string `json:"author"`
		} `json:"posts"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Posts) != 30 {
		t.Fatalf("wanted 30 posts got %d", len(output.Posts))
	}
}
