package auth

import "context"

type contextKey string

const userContextKey contextKey = "vaulx_user"

type UserContext struct {
	ID    string
	Email string
	Name  string
	Role  string
}

func SetCurrentUser(ctx context.Context, u UserContext) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}

func GetCurrentUser(ctx context.Context) (UserContext, bool) {
	u, ok := ctx.Value(userContextKey).(UserContext)
	return u, ok
}
