// Package transport wires the posts endpoints to net/http using go-kit's
// transport/http.Server. This is the only place (besides endpoint) allowed
// to import go-kit, and the only place that knows about JSON wire format.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	httptransport "github.com/go-kit/kit/transport/http"

	"github.com/mangopep/forgestack/internal/modules/posts/endpoint"
	"github.com/mangopep/forgestack/internal/modules/posts/service"
	"github.com/mangopep/forgestack/internal/platform/apierror"
)

// NewCreateHandler returns the http.Handler for POST /api/v1/posts.
// Request body size limiting is applied by the caller (see
// internal/platform/middleware.MaxBytes) since go-kit's DecodeRequestFunc
// does not receive the http.ResponseWriter that http.MaxBytesReader needs.
func NewCreateHandler(svc service.Service) http.Handler {
	return httptransport.NewServer(
		endpoint.MakeCreatePostEndpoint(svc),
		decodeCreateRequest,
		encodeCreated,
		httptransport.ServerErrorEncoder(encodeError),
	)
}

// NewListHandler returns the http.Handler for GET /api/v1/posts.
func NewListHandler(svc service.Service) http.Handler {
	return httptransport.NewServer(
		endpoint.MakeListPostsEndpoint(svc),
		decodeListRequest,
		encodeOK,
		httptransport.ServerErrorEncoder(encodeError),
	)
}

func decodeCreateRequest(_ context.Context, r *http.Request) (interface{}, error) {
	var body endpoint.CreatePostRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return nil, apierror.Bad(errors.New("invalid JSON or request too large"))
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return nil, apierror.Bad(errors.New("only one JSON object allowed"))
	}
	return body, nil
}

func decodeListRequest(_ context.Context, r *http.Request) (interface{}, error) {
	limit := service.UnsetLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return nil, apierror.Bad(errors.New("limit must be a number"))
		}
		limit = parsed
	}
	return endpoint.ListPostsRequest{Limit: limit}, nil
}

func setJSONHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func encodeCreated(_ context.Context, w http.ResponseWriter, response interface{}) error {
	setJSONHeaders(w)
	w.WriteHeader(http.StatusCreated)
	return json.NewEncoder(w).Encode(response)
}

func encodeOK(_ context.Context, w http.ResponseWriter, response interface{}) error {
	setJSONHeaders(w)
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(response)
}

// encodeError maps errors to HTTP responses without ever leaking an
// unclassified error's message to the client.
func encodeError(_ context.Context, err error, w http.ResponseWriter) {
	setJSONHeaders(w)
	var bad apierror.BadRequest
	if errors.As(err, &bad) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": bad.Error()})
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "internal error"})
}
