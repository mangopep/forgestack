package service

import (
	"context"
	"errors"

	"testing"
	"time"

	"github.com/mangopep/forgestack/internal/modules/posts/domain"
)

// fakeRepository is a minimal, deterministic test double — no real storage,
// no HTTP, proving the service is testable in complete isolation.
type fakeRepository struct {
	posts  []domain.Post
	nextID int64
}

func (f *fakeRepository) Add(_ context.Context, build func(id int64) (domain.Post, error)) (domain.Post, error) {
	f.nextID++
	p, err := build(f.nextID)
	if err != nil {
		f.nextID--
		return domain.Post{}, err
	}
	f.posts = append(f.posts, p)
	return p, nil
}

func (f *fakeRepository) List(_ context.Context, limit int) []domain.Post {
	if limit > len(f.posts) {
		limit = len(f.posts)
	}
	return f.posts[:limit]
}

func fixedClock(t time.Time) Clock { return func() time.Time { return t } }

func TestCreatePost(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := New(&fakeRepository{}, fixedClock(now))
	p, err := svc.CreatePost(context.Background(), "Nia", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if p.Author != "Nia" || !p.CreatedAt.Equal(now) {
		t.Errorf("unexpected post: %+v", p)
	}
}

func TestCreatePostValidationError(t *testing.T) {
	svc := New(&fakeRepository{}, nil)
	_, err := svc.CreatePost(context.Background(), "", "hello")
	if !errors.Is(err, domain.ErrAuthorRequired) {

	if err != domain.ErrAuthorRequired {

		t.Fatalf("expected ErrAuthorRequired, got %v", err)
	}
	if !IsValidationError(err) {
		t.Error("expected IsValidationError(err) to be true")
	}
}

func TestListPostsDefaultLimit(t *testing.T) {
	repo := &fakeRepository{}
	svc := New(repo, nil)
	for i := 0; i < 25; i++ {
		if _, err := svc.CreatePost(context.Background(), "a", "b"); err != nil {
			t.Fatal(err)
		}
	}
	posts, err := svc.ListPosts(context.Background(), UnsetLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != DefaultListLimit {
		t.Fatalf("expected default limit %d, got %d", DefaultListLimit, len(posts))
	}
}

func TestListPostsRejectsOutOfRangeLimit(t *testing.T) {
	svc := New(&fakeRepository{}, nil)
	for _, limit := range []int{0, -2, 101, 1000} {
		_, err := svc.ListPosts(context.Background(), limit)

		if !errors.Is(err, ErrInvalidLimit) {

		if err != ErrInvalidLimit {

			t.Errorf("limit=%d: expected ErrInvalidLimit, got %v", limit, err)
		}
	}
}

func TestListPostsAcceptsBoundaryLimits(t *testing.T) {
	svc := New(&fakeRepository{}, nil)
	for _, limit := range []int{MinListLimit, MaxListLimit} {
		if _, err := svc.ListPosts(context.Background(), limit); err != nil {
			t.Errorf("limit=%d: expected no error, got %v", limit, err)
		}
	}
}
