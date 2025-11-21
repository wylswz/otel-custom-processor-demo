package simpleprocessor

import (
	"context"
	"fmt"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

type simpleProcessor struct {
	logger *zap.Logger
	next   consumer.Metrics
}

func newProcessor(logger *zap.Logger, next consumer.Metrics) *simpleProcessor {
	return &simpleProcessor{
		logger: logger,
		next:   next,
	}
}

func (p *simpleProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		rm := md.ResourceMetrics().At(i)
		for j := 0; j < rm.ScopeMetrics().Len(); j++ {
			sm := rm.ScopeMetrics().At(j)
			for k := 0; k < sm.Metrics().Len(); k++ {
				if sm.Metrics().At(k).Type() == pmetric.MetricTypeSum {
					sum := sm.Metrics().At(k).Sum()
					for l := 0; l < sum.DataPoints().Len(); l++ {
						dp := sum.DataPoints().At(l)
						dp.SetIntValue(666)
						fmt.Printf("==== Data Point: %v ====\n", dp)
					}
				}
			}
		}
	}
	return p.next.ConsumeMetrics(ctx, md)
}

func (p *simpleProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func (p *simpleProcessor) Start(ctx context.Context, host component.Host) error {
	return nil
}

func (p *simpleProcessor) Shutdown(ctx context.Context) error {
	return nil
}
