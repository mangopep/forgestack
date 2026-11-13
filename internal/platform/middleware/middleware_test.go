package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCorrelationIDGeneratesWhenAbsent(t *testing.T) {
	var seen string
	handler := CorrelationID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if seen == "" {
		t.Fatal("expected a generated request ID in the handler's context, got empty string")
	}
	if got := rec.Header().Get(RequestIDHeader); got != seen {
		t.Errorf("response header %s = %q, want it to match the context value %q", RequestIDHeader, got, seen)
	}
}

func TestCorrelationIDReusesInboundValue(t *testing.T) {
	const inbound = "caller-supplied-trace-id"
	var seen string
	handler := CorrelationID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(RequestIDHeader, inbound)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if seen != inbound {
		t.Errorf("expected the inbound request ID to be reused, got %q, want %q", seen, inbound)
	}
	if got := rec.Header().Get(RequestIDHeader); got != inbound {
		t.Errorf("response header %s = %q, want %q (echoed back)", RequestIDHeader, got, inbound)
	}
}

func TestRequestIDWithoutMiddlewareReturnsEmpty(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if got := RequestID(req.Context()); got != "" {
		t.Errorf("expected empty string for a context never touched by CorrelationID, got %q", got)
	}
}

func TestTwoRequestsGetDifferentGeneratedIDs(t *testing.T) {
	var first, second string
	handler := CorrelationID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first == "" {
			first = RequestID(r.Context())
			return
		}
		second = RequestID(r.Context())
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if first == "" || second == "" {
		t.Fatal("expected both requests to get a non-empty generated ID")
	}
	if first == second {
		t.Error("expected two separate requests with no inbound header to get different generated IDs, got the same one twice")
	}
}
