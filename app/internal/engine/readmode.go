package engine

import "context"

type indexOnlyKey struct{}

// WithIndexOnly requests list metadata only for this read. It must never be used
// for a conversation view or copy; no mode is stored on a shared connector.
func WithIndexOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, indexOnlyKey{}, true)
}

func IsIndexOnly(ctx context.Context) bool {
	v, _ := ctx.Value(indexOnlyKey{}).(bool)
	return v
}
