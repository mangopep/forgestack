package endpoint

import (
	"context"
	"errors"
	"testing"

	"github.com/mangopep/forgestack/internal/modules/posts/repository"
	"github.com/mangopep/forgestack/internal/modules/posts/service"
	"github.com/mangopep/forgestack/internal/platform/apierror"
)

// These tests call the endpoint functions directly — no http.Server, no
// httptest, proving endpoints are testable in isolation from transport.

func TestCreatePostEndpoint(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	ep := MakeCreatePostEndpoint(svc)
	resp, err := ep(context.Background(), CreatePostRequest{Author: "Nia", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	post, ok := resp.(PostResponse)
	if !ok || post.Author != "Nia" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestCreatePostEndpointClassifiesValidationError(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	ep := MakeCreatePostEndpoint(svc)
	_, err := ep(context.Background(), CreatePostRequest{Author: "", Text: "hello"})
	var bad apierror.BadRequest
	if !errors.As(err, &bad) {
		t.Fatalf("expected a classified apierror.BadRequest, got %v (%T)", err, err)
	}
}

func TestListPostsEndpoint(t *testing.T) {
	svc := service.New(repository.NewInMemory(), nil)
	createEp := MakeCreatePostEndpoint(svc)
	if _, err := createEp(context.Background(), CreatePostRequest{Author: "a", Text: "b"}); err != nil {
		t.Fatal(err)
	}
	listEp := MakeListPostsEndpoint(svc)
	resp, err := listEp(context.Background(), ListPostsRequest{Limit: service.UnsetLimit})
	if err != nil {
		t.Fatal(err)
	}
	out, ok := resp.(ListPostsResponse)
	if !ok || len(out.Posts) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
