package transport

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/mangopep/forgestack/internal/modules/posts/repository"
	"github.com/mangopep/forgestack/internal/modules/posts/service"
)

func TestCreateHandlerSuccess(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	h := NewCreateHandler(svc)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"author":"Nia","text":"hi"}`)))
	if r.Code != 201 {
		t.Fatalf("want 201, got %d: %s", r.Code, r.Body.String())
	}
	if ct := r.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("unexpected content type %q", ct)
	}
}

func TestCreateHandlerValidationError(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	h := NewCreateHandler(svc)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"author":"","text":"hi"}`)))
	if r.Code != 400 {
		t.Fatalf("want 400, got %d: %s", r.Code, r.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] == "" {
		t.Error("expected a non-empty error message in the response body")
	}
}

func TestCreateHandlerRejectsUnknownFields(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	h := NewCreateHandler(svc)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"author":"a","text":"b","admin":true}`)))
	if r.Code != 400 {
		t.Fatalf("want 400 for unknown field, got %d: %s", r.Code, r.Body.String())
	}
}

func TestCreateHandlerRejectsTrailingData(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	h := NewCreateHandler(svc)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"author":"a","text":"b"}{"extra":1}`)))
	if r.Code != 400 {
		t.Fatalf("want 400 for trailing JSON, got %d: %s", r.Code, r.Body.String())
	}
}

func TestListHandler(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	createH := NewCreateHandler(svc)
	r := httptest.NewRecorder()
	createH.ServeHTTP(r, httptest.NewRequest("POST", "/", bytes.NewBufferString(`{"author":"a","text":"b"}`)))
	if r.Code != 201 {
		t.Fatalf("setup failed: %d", r.Code)
	}

	listH := NewListHandler(svc)
	r = httptest.NewRecorder()
	listH.ServeHTTP(r, httptest.NewRequest("GET", "/?limit=1", nil))
	if r.Code != 200 {
		t.Fatalf("want 200, got %d: %s", r.Code, r.Body.String())
	}
	var out struct {
		Posts []map[string]any `json:"posts"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(out.Posts))
	}
}

func TestListHandlerRejectsOutOfRangeLimit(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	h := NewListHandler(svc)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/?limit=999", nil))
	if r.Code != 400 {
		t.Fatalf("want 400, got %d: %s", r.Code, r.Body.String())
	}
}

func TestListHandlerRejectsNonNumericLimit(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	h := NewListHandler(svc)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/?limit=abc", nil))
	if r.Code != 400 {
		t.Fatalf("want 400, got %d: %s", r.Code, r.Body.String())
	}
}
