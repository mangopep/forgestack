// Package service holds the posts module's application logic: orchestrates
// domain validation and repository calls. It depends on the repository
// interface only, so it can be tested with a fake store and no HTTP server.
package service

import (
	"context"
	"errors"
	"time"

	"github.com/mangopep/forgestack/internal/modules/posts/domain"
	"github.com/mangopep/forgestack/internal/modules/posts/repository"
)

// List limit bounds, preserved from the original implementation.
const (
	DefaultListLimit = 20
	MinListLimit     = 1
	MaxListLimit     = 100
	// UnsetLimit tells ListPosts "no limit was requested, use the default."
	// It must stay outside the valid 1..100 range so it can never collide
	// with a real, explicitly-requested limit (including an explicit 0).
	UnsetLimit = -1
)

var ErrInvalidLimit = errors.New("limit must be between 1 and 100")

// Clock lets tests control "now" without sleeping or depending on wall time.
type Clock func() time.Time

type Service interface {
	CreatePost(ctx context.Context, author, text string) (domain.Post, error)
	ListPosts(ctx context.Context, limit int) ([]domain.Post, error)
}

type service struct {
	repo  repository.Repository
	clock Clock
}

// New constructs a Service. A nil clock defaults to time.Now().UTC().
func New(repo repository.Repository, clock Clock) Service {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &service{repo: repo, clock: clock}
}

func (s *service) CreatePost(ctx context.Context, author, text string) (domain.Post, error) {
	return s.repo.Add(ctx, func(id int64) (domain.Post, error) {
		return domain.NewPost(id, author, text, s.clock())
	})
}

func (s *service) ListPosts(ctx context.Context, limit int) ([]domain.Post, error) {
	if limit == UnsetLimit {
		limit = DefaultListLimit
	}
	if limit < MinListLimit || limit > MaxListLimit {
		return nil, ErrInvalidLimit
	}
	return s.repo.List(ctx, limit), nil
}

// IsValidationError reports whether err is a known client-input validation
// failure (as opposed to an unexpected internal error). The transport layer
// uses this to decide between a safe 400 and a generic 500 — never by
// string-matching error text.
func IsValidationError(err error) bool {
	switch {
	case errors.Is(err, domain.ErrAuthorRequired),
		errors.Is(err, domain.ErrAuthorTooLong),
		errors.Is(err, domain.ErrTextRequired),
		errors.Is(err, domain.ErrTextTooLong),
		errors.Is(err, ErrInvalidLimit):
		return true
	}
	return false
}
