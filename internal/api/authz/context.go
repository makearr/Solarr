package authz

import "context"

// contextKey is unexported so nothing outside this package can put an Access
// into a context, or take one out without going through FromContext.
type contextKey struct{}

// WithAccess attaches an Access to a request context. Middleware does this
// once; everything downstream reads it.
func WithAccess(ctx context.Context, access Access) context.Context {
	return context.WithValue(ctx, contextKey{}, access)
}

// FromContext returns the Access a request is running under (Invariant 36).
func FromContext(ctx context.Context) (Access, error) {
	access, ok := ctx.Value(contextKey{}).(Access)
	if !ok {
		return Access{}, ErrNoAccess
	}
	return access, nil
}
