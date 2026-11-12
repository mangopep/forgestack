package apierror

import (
	"errors"
	"testing"
)

func TestBadRequestUnwrap(t *testing.T) {
	inner := errors.New("boom")
	bad := Bad(inner)
	if bad.Error() != "boom" {
		t.Errorf("unexpected Error(): %q", bad.Error())
	}
	if !errors.Is(bad, inner) {
		t.Error("expected errors.Is to see through to the wrapped error via Unwrap")
	}
}
