// Package repository is the posts module's persistence boundary. The
// service layer depends on the Repository interface, never on a concrete
// store, so a future provider (e.g. Postgres) can implement it without
// touching business logic or its tests.
package repository

import (
	"context"
	"sync"

	"github.com/mangopep/forgestack/internal/modules/posts/domain"
)

// Repository is the persistence boundary for posts.
type Repository interface {
	// Add atomically allocates the next ID and stores a post built by build.
	// build performs validation (via domain.NewPost); if it returns an
	// error, nothing is stored and no ID is consumed in a way callers can
	// observe. This keeps ID-allocation and append atomic together, exactly
	// as the single-mutex original implementation did, while keeping the
	// repository itself free of domain validation rules.
	Add(ctx context.Context, build func(id int64) (domain.Post, error)) (domain.Post, error)
	List(ctx context.Context, limit int) []domain.Post
}

// InMemory is a concurrency-safe in-memory Repository. It is explicitly a
// lab/demo implementation — data is lost on restart — documented as such
// rather than implied to be production storage.
type InMemory struct {
	mu     sync.RWMutex
	nextID int64
	posts  []domain.Post
}

func NewInMemory() *InMemory { return &InMemory{} }

func (r *InMemory) Add(_ context.Context, build func(id int64) (domain.Post, error)) (domain.Post, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, err := build(r.nextID + 1)
	if err != nil {
		return domain.Post{}, err
	}
	r.nextID++
	r.posts = append(r.posts, p)
	return p, nil
}

func (r *InMemory) List(_ context.Context, limit int) []domain.Post {
	r.mu.RLock()
	defer r.mu.RUnlock()
	start := len(r.posts) - limit
	if start < 0 {
		start = 0
	}
	out := make([]domain.Post, len(r.posts[start:]))
	copy(out, r.posts[start:])
	return out
}
