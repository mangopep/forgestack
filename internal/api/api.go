// Package api implements the first lab's intentionally small, concurrency-safe
// HTTP API. The store is in memory; connecting PostgreSQL is a later lab.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type post struct {
	ID        int64     `json:"id"`
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}
type store struct {
	mu     sync.RWMutex
	nextID int64
	posts  []post
}

func (s *store) add(author, text string) post {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	p := post{ID: s.nextID, Author: author, Text: text, CreatedAt: time.Now().UTC()}
	s.posts = append(s.posts, p)
	return p
}
func (s *store) list(limit int) []post {
	s.mu.RLock()
	defer s.mu.RUnlock()
	start := len(s.posts) - limit
	if start < 0 {
		start = 0
	}
	result := make([]post, len(s.posts[start:]))
	copy(result, s.posts[start:])
	return result
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func NewHandler() http.Handler {
	s := &store{}
	var requestCount atomic.Uint64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "service": "crushly-api"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ready", "storage": "in-memory"})
	})
	mux.HandleFunc("GET /api/v1/posts", func(w http.ResponseWriter, r *http.Request) {
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 100 {
				writeJSON(w, 400, map[string]string{"error": "limit must be between 1 and 100"})
				return
			}
			limit = parsed
		}
		writeJSON(w, 200, map[string]any{"posts": s.list(limit)})
	})
	mux.HandleFunc("POST /api/v1/posts", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var body struct {
			Author string `json:"author"`
			Text   string `json:"text"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid JSON or request too large"})
			return
		}
		var extra any
		if err := dec.Decode(&extra); err == nil {
			writeJSON(w, 400, map[string]string{"error": "only one JSON object allowed"})
			return
		}
		body.Author = strings.TrimSpace(body.Author)
		body.Text = strings.TrimSpace(body.Text)
		if body.Author == "" || len(body.Author) > 80 || body.Text == "" || len(body.Text) > 500 {
			writeJSON(w, 400, map[string]string{"error": "author 1-80 bytes and text 1-500 bytes required"})
			return
		}
		writeJSON(w, 201, s.add(body.Author, body.Text))
	})
	mux.HandleFunc("GET /metrics-demo", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"requests_seen": requestCount.Load(), "note": "demo counter; real metrics in observability lab"})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestCount.Add(1)
		rw := &responseWriter{ResponseWriter: w, status: 200}
		mux.ServeHTTP(rw, r)
		slog.Info("http_request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
