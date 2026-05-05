package global

import (
	"sync"

	"github.com/cvewatcher/mulval/pkg/monitoring"
)

var (
	metricsInstance *monitoring.Metrics
	metricsOnce     sync.Once
)

func InitMetrics() (err error) {
	metricsOnce.Do(func() {
		metricsInstance, err = monitoring.NewMetrics(Meter)
	})
	return
}

func GetMetrics() *monitoring.Metrics {
	return metricsInstance
}
