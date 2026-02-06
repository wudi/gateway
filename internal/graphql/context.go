package graphql

import "context"

type contextKey struct{}

// WithInfo stores GraphQLInfo in the request context.
func WithInfo(ctx context.Context, info *GraphQLInfo) context.Context {
	return context.WithValue(ctx, contextKey{}, info)
}

// GetInfo retrieves GraphQLInfo from the request context. Returns nil for non-GraphQL requests.
func GetInfo(ctx context.Context) *GraphQLInfo {
	v, _ := ctx.Value(contextKey{}).(*GraphQLInfo)
	return v
}
