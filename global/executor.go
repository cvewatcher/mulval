package global

import (
	"context"

	"github.com/cvewatcher/mulval/pkg/executor"
)

var (
	executorInstance *executor.Executor
)

func InitExecutor(ctx context.Context) (_ error) {
	executorInstance = executor.New(ctx, executor.ExecutorConfig{
		Logger:       Log().Sub,
		Metrics:      GetMetrics(),
		Tracer:       Tracer,
		PgsqlManager: GetPgSQLManager(),
		NatsManager:  GetNatsManager(),
	})
	return
}

func GetExecutor() *executor.Executor {
	return executorInstance
}
