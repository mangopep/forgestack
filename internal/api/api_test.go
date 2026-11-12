package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestHealth(t *testing.T) {
	r := httptest.NewRecorder()
	NewHandler().ServeHTTP(r, httptest.NewRequest("GET", "/healthz", nil))
	if r.Code != 200 {
		t.Fatalf("want 200, got %d", r.Code)
	}
	if r.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatal("missing JSON content type")
	}
}
func TestPostLifecycle(t *testing.T) {
	handler := NewHandler()
	data := []byte(`{"author":"Nia","text":"hello Alex"}`)
	r := httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("POST", "/api/v1/posts", bytes.NewReader(data)))
	if r.Code != 201 {
		t.Fatalf("want 201 got %d: %s", r.Code, r.Body.String())
	}
	r = httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/posts?limit=1", nil))
	var out struct {
		Posts []post `json:"posts"`
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
		NewHandler().ServeHTTP(r, httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body)))
		if r.Code != 400 {
			t.Fatalf("%s %s: want 400 got %d", tt.method, tt.path, r.Code)
		}
	}
}
func TestConcurrentRequests(t *testing.T) {
	h := NewHandler()
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
		Posts []post `json:"posts"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Posts) != 30 {
		t.Fatalf("wanted 30 posts got %d", len(output.Posts))
	}
}
