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

// BatchInfo holds information about a batched GraphQL request.
type BatchInfo struct {
	Size    int
	Mode    string
	Queries []*GraphQLInfo
}

type batchContextKey struct{}

// WithBatchInfo stores BatchInfo in the request context.
func WithBatchInfo(ctx context.Context, info *BatchInfo) context.Context {
	return context.WithValue(ctx, batchContextKey{}, info)
}

// GetBatchInfo retrieves BatchInfo from the request context. Returns nil for non-batch requests.
func GetBatchInfo(ctx context.Context) *BatchInfo {
	v, _ := ctx.Value(batchContextKey{}).(*BatchInfo)
	return v
}
