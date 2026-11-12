package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mangopep/forgestack/internal/modules/posts/domain"
)

func TestInMemoryAddAndList(t *testing.T) {
	r := NewInMemory()
	ctx := context.Background()
	_, err := r.Add(ctx, func(id int64) (domain.Post, error) {
		return domain.NewPost(id, "a", "b", time.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	got := r.List(ctx, 10)
	if len(got) != 1 {
		t.Fatalf("expected 1 post, got %d", len(got))
	}
}

func TestInMemoryAddPropagatesBuildError(t *testing.T) {
	r := NewInMemory()
	ctx := context.Background()
	_, err := r.Add(ctx, func(id int64) (domain.Post, error) {
		return domain.NewPost(id, "", "b", time.Now()) // invalid: empty author
	})
	if !errors.Is(err, domain.ErrAuthorRequired) {

	if err != domain.ErrAuthorRequired {

		t.Fatalf("expected ErrAuthorRequired, got %v", err)
	}
	if got := r.List(ctx, 10); len(got) != 0 {
		t.Fatalf("a failed build must not be stored, got %d posts", len(got))
	}
}

func TestInMemoryListIsBounded(t *testing.T) {
	r := NewInMemory()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := r.Add(ctx, func(id int64) (domain.Post, error) {
			return domain.NewPost(id, "a", "b", time.Now())
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.List(ctx, 2); len(got) != 2 {
		t.Fatalf("expected limit to bound results to 2, got %d", len(got))
	}
	if got := r.List(ctx, 100); len(got) != 5 {
		t.Fatalf("expected a limit larger than the dataset to return all 5, got %d", len(got))
	}
}

// TestInMemoryConcurrentAdd mirrors the concurrency guarantee the original
// single-file store provided: concurrent writers never lose an entry and
// never corrupt state. Run with -race.
func TestInMemoryConcurrentAdd(t *testing.T) {
	r := NewInMemory()
	ctx := context.Background()
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Add(ctx, func(id int64) (domain.Post, error) {
				return domain.NewPost(id, "concurrent", "post", time.Now())
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got := r.List(ctx, n)
	if len(got) != n {
		t.Fatalf("expected %d posts after concurrent adds, got %d", n, len(got))
	}
	seen := map[int64]bool{}
	for _, p := range got {
		if seen[p.ID] {
			t.Fatalf("duplicate ID %d — IDs are not unique under concurrency", p.ID)
		}
		seen[p.ID] = true
	}
}
