// Package domain holds the posts module's pure business rules: no HTTP, no
// storage, no go-kit. It is testable without starting anything.
package domain

import (
	"errors"
	"strings"
	"time"
)

// Length limits, preserved from the original single-file implementation.
const (
	MaxAuthorLen = 80
	MaxTextLen   = 500
)

var (
	ErrAuthorRequired = errors.New("author is required")
	ErrAuthorTooLong  = errors.New("author must be at most 80 bytes")
	ErrTextRequired   = errors.New("text is required")
	ErrTextTooLong    = errors.New("text must be at most 500 bytes")
)

// Post is the posts module's core entity.
type Post struct {
	ID        int64
	Author    string
	Text      string
	CreatedAt time.Time
}

// NewPost validates input and constructs a Post. It never touches a clock
// other than the one passed in, so it is deterministic and testable.
func NewPost(id int64, author, text string, now time.Time) (Post, error) {
	author = strings.TrimSpace(author)
	text = strings.TrimSpace(text)
	switch {
	case author == "":
		return Post{}, ErrAuthorRequired
	case len(author) > MaxAuthorLen:
		return Post{}, ErrAuthorTooLong
	case text == "":
		return Post{}, ErrTextRequired
	case len(text) > MaxTextLen:
		return Post{}, ErrTextTooLong
	}
	return Post{ID: id, Author: author, Text: text, CreatedAt: now}, nil
}
