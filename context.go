package vignet

import "context"

type ctxKey int

const (
	authCtxKey ctxKey = iota
)

func ctxWithAuthCtx(ctx context.Context, authCtx AuthCtx) context.Context {
	return context.WithValue(ctx, authCtxKey, authCtx)
}

func authCtxFromCtx(ctx context.Context) AuthCtx {
	return ctx.Value(authCtxKey).(AuthCtx)
}
