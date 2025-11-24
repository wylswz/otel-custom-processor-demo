package simpleprocessor

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
)

type simpleProcessor struct {
	logger *zap.Logger
	next   consumer.Metrics

	mu             sync.Mutex
	aggregations   map[string]int64 // Aggregates sums by work.type
	done           chan struct{}
	checkpointFile string
}

func newProcessor(logger *zap.Logger, next consumer.Metrics, checkpointFile string) *simpleProcessor {
	return &simpleProcessor{
		logger:         logger,
		next:           next,
		aggregations:   make(map[string]int64),
		done:           make(chan struct{}),
		checkpointFile: checkpointFile,
	}
}

func (p *simpleProcessor) ConsumeMetrics(ctx context.Context, md pmetric.Metrics) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := 0; i < md.ResourceMetrics().Len(); i++ {
		rm := md.ResourceMetrics().At(i)
		for j := 0; j < rm.ScopeMetrics().Len(); j++ {
			sm := rm.ScopeMetrics().At(j)
			for k := 0; k < sm.Metrics().Len(); k++ {
				metric := sm.Metrics().At(k)
				if metric.Type() == pmetric.MetricTypeSum {
					sum := metric.Sum()
					for l := 0; l < sum.DataPoints().Len(); l++ {
						dp := sum.DataPoints().At(l)
						// For demo: Aggregate by 'work.type', ignoring unique 'work.id'
						if workType, ok := dp.Attributes().Get("work.type"); ok {
							p.aggregations[workType.Str()] += dp.IntValue()
						}
					}
				}
			}
		}
	}
	// Swallow incoming metrics (batching them)
	return nil
}

func (p *simpleProcessor) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: true}
}

func (p *simpleProcessor) Start(ctx context.Context, host component.Host) error {
	p.loadState()
	go p.flushLoop()
	return nil
}

func (p *simpleProcessor) Shutdown(ctx context.Context) error {
	close(p.done)
	p.saveState()
	return nil
}

func (p *simpleProcessor) loadState() {
	if p.checkpointFile == "" {
		return
	}
	data, err := os.ReadFile(p.checkpointFile)
	if err != nil {
		if !os.IsNotExist(err) {
			p.logger.Error("Failed to read checkpoint file", zap.Error(err))
		}
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := json.Unmarshal(data, &p.aggregations); err != nil {
		p.logger.Error("Failed to unmarshal checkpoint", zap.Error(err))
	}
}

func (p *simpleProcessor) saveState() {
	if p.checkpointFile == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saveStateLocked()
}

func (p *simpleProcessor) saveStateLocked() {
	data, err := json.Marshal(p.aggregations)
	if err != nil {
		p.logger.Error("Failed to marshal checkpoint", zap.Error(err))
		return
	}
	if err := os.WriteFile(p.checkpointFile, data, 0644); err != nil {
		p.logger.Error("Failed to write checkpoint file", zap.Error(err))
	}
}

func (p *simpleProcessor) flushLoop() {
	// Flush every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.flush()
		}
	}
}

func (p *simpleProcessor) flush() {
	p.mu.Lock()
	if p.checkpointFile != "" {
		p.saveStateLocked()
	}

	if len(p.aggregations) == 0 {
		p.mu.Unlock()
		return
	}

	// Construct new metrics batch
	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.Scope().SetName("simple-aggregator")

	m := sm.Metrics().AppendEmpty()
	m.SetName("work_done_batched")
	m.SetUnit("1")
	sum := m.SetEmptySum()
	sum.SetIsMonotonic(true)
	sum.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)

	for workType, count := range p.aggregations {
		dp := sum.DataPoints().AppendEmpty()
		dp.Attributes().PutStr("work.type", workType)
		dp.SetIntValue(count)
	}

	// Do not reset aggregations for cumulative metrics
	p.mu.Unlock()

	// Use background context as the original request context is long gone
	if err := p.next.ConsumeMetrics(context.Background(), md); err != nil {
		p.logger.Error("Failed to flush metrics", zap.Error(err))
	}
}
