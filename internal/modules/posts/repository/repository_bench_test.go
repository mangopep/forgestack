package repository

import (
	"context"
	"testing"
	"time"

	"github.com/mangopep/forgestack/internal/modules/posts/domain"
)

// BenchmarkInMemoryAdd establishes a baseline for the repository's hot
// path. Run with: go test -bench=. -benchmem ./internal/modules/posts/repository
func BenchmarkInMemoryAdd(b *testing.B) {
	r := NewInMemory()
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		_, err := r.Add(ctx, func(id int64) (domain.Post, error) {
			return domain.NewPost(id, "bench", "payload", time.Now())
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
