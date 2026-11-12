// Package endpoint wraps the posts service in go-kit's endpoint.Endpoint
// abstraction (context.Context, interface{}, interface{}, error). This is
// the only place in the posts module allowed to import go-kit — domain and
// service stay free of it. An endpoint can be called and tested directly,
// with no HTTP server involved.
package endpoint

import (
	"context"
	"time"

	kitendpoint "github.com/go-kit/kit/endpoint"

	"github.com/mangopep/forgestack/internal/modules/posts/domain"
	"github.com/mangopep/forgestack/internal/modules/posts/service"
	"github.com/mangopep/forgestack/internal/platform/apierror"
)

type CreatePostRequest struct {
	Author string `json:"author"`
	Text   string `json:"text"`
}

type ListPostsRequest struct {
	// Limit is service.UnsetLimit when the caller didn't specify one.
	Limit int
}

type PostResponse struct {
	ID        int64  `json:"id"`
	Author    string `json:"author"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

type ListPostsResponse struct {
	Posts []PostResponse `json:"posts"`
}

func toResponse(p domain.Post) PostResponse {
	return PostResponse{
		ID:        p.ID,
		Author:    p.Author,
		Text:      p.Text,
		CreatedAt: p.CreatedAt.Format(time.RFC3339),
	}
}

// classify turns a known validation error into a client-safe apierror.Bad;
// anything else passes through unchanged (the transport layer maps
// unclassified errors to a generic 500, never leaking their text).
func classify(err error) error {
	if err == nil {
		return nil
	}
	if service.IsValidationError(err) {
		return apierror.Bad(err)
	}
	return err
}

// MakeCreatePostEndpoint adapts Service.CreatePost to kitendpoint.Endpoint.
func MakeCreatePostEndpoint(svc service.Service) kitendpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(CreatePostRequest)
		p, err := svc.CreatePost(ctx, req.Author, req.Text)
		if err != nil {
			return nil, classify(err)
		}
		return toResponse(p), nil
	}
}

// MakeListPostsEndpoint adapts Service.ListPosts to kitendpoint.Endpoint.
func MakeListPostsEndpoint(svc service.Service) kitendpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		req := request.(ListPostsRequest)
		posts, err := svc.ListPosts(ctx, req.Limit)
		if err != nil {
			return nil, classify(err)
		}
		out := make([]PostResponse, len(posts))
		for i, p := range posts {
			out[i] = toResponse(p)
		}
		return ListPostsResponse{Posts: out}, nil
	}
}
