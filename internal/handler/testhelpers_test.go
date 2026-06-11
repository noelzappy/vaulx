package handler_test

import (
	"context"

	"github.com/go-chi/chi/v5"
)

func withChiParam(ctx context.Context, key, val string) context.Context {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return context.WithValue(ctx, chi.RouteCtxKey, rctx)
}
