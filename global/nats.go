package global

import (
	"context"

	"github.com/cvewatcher/mulval/pkg/services/nats"
)

var (
	natsManager *nats.Manager
)

func InitNatsManager(ctx context.Context) (err error) {
	natsManager, err = nats.NewManager(ctx, nats.ManagerConfig{
		URL:        Config.Events.URL,
		InstanceID: Config.Events.InstanceID.Content,
		Logger:     Log().Sub,
		Tracer:     Tracer,
		Metrics:    GetMetrics(),
	})
	return
}

func GetNatsManager() *nats.Manager {
	return natsManager
}
