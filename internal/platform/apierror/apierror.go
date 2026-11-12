// Package apierror marks errors as safe to expose to API clients. Anything
// not wrapped here is treated as internal and never has its message sent
// to a client — only a generic "internal error" is, so unexpected failures
// never leak internals (stack traces, paths, driver errors, etc.).
package apierror

// BadRequest marks err as the client's fault: its Error() text is safe to
// return in an HTTP 400 response body.
type BadRequest struct{ err error }

// Bad wraps err as a client-caused error.
func Bad(err error) BadRequest { return BadRequest{err: err} }

func (b BadRequest) Error() string { return b.err.Error() }
func (b BadRequest) Unwrap() error { return b.err }
