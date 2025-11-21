package simpleprocessor

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/processor"
)

var (
	Type = component.MustNewType("simple")
)

// NewFactory creates a factory for the simple processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		Type,
		createDefaultConfig,
		processor.WithMetrics(createMetricsProcessor, component.StabilityLevelDevelopment),
	)
}

func createDefaultConfig() component.Config {
	return &Config{}
}

// Config represents the configuration for the simple processor.
type Config struct{}

func createMetricsProcessor(
	_ context.Context,
	set processor.Settings,
	_ component.Config,
	nextConsumer consumer.Metrics,
) (processor.Metrics, error) {
	return newProcessor(set.Logger, nextConsumer), nil
}
