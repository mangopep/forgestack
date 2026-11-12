package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewPostValid(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p, err := NewPost(1, "  Nia  ", "  hello  ", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Author != "Nia" || p.Text != "hello" {
		t.Errorf("expected trimmed fields, got %+v", p)
	}
	if p.ID != 1 || !p.CreatedAt.Equal(now) {
		t.Errorf("expected id=1 and the passed-in clock value, got %+v", p)
	}
}

func TestNewPostValidation(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name, author, text string
		wantErr            error
	}{
		{"empty author", "", "ok", ErrAuthorRequired},
		{"whitespace-only author", "   ", "ok", ErrAuthorRequired},
		{"author too long", strings.Repeat("a", MaxAuthorLen+1), "ok", ErrAuthorTooLong},
		{"author at limit is fine", strings.Repeat("a", MaxAuthorLen), "ok", nil},
		{"empty text", "author", "", ErrTextRequired},
		{"text too long", "author", strings.Repeat("x", MaxTextLen+1), ErrTextTooLong},
		{"text at limit is fine", "author", strings.Repeat("x", MaxTextLen), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewPost(1, c.author, c.text, now)
			if c.wantErr == nil && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
			if c.wantErr != nil && err != c.wantErr {
				t.Fatalf("expected %v, got %v", c.wantErr, err)
			}
		})
	}
}
