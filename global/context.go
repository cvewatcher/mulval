package global

import "context"

func WithOperationName(ctx context.Context, opName string) context.Context {
	if opName == "" {
		return ctx
	}
	return context.WithValue(ctx, operationKey{}, opName)
}

func OperationNameFromContext(ctx context.Context) string {
	val := ctx.Value(operationKey{})
	if val == nil {
		return ""
	}
	return val.(string)
}
