package mymiddleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sony/gobreaker/v2"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/extension/extensionmiddleware"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	_ extensionmiddleware.GRPCClient = &ext{}
	_ extensionmiddleware.HTTPClient = &ext{}
)

// ErrCircuitBreakerOpen is returned when the circuit breaker is open
var ErrCircuitBreakerOpen = errors.New("circuit breaker is open")

type Config struct {
	// MaxRequests is the maximum number of requests allowed to pass through
	// when the circuit breaker is half-open. If MaxRequests is 0, it will be set to 1.
	MaxRequests uint32 `mapstructure:"max_requests"`

	// Interval is the cyclic period of the closed state for the circuit breaker
	// to clear the internal counts. If Interval is 0, it will be set to 60 seconds.
	Interval time.Duration `mapstructure:"interval"`

	// Timeout is the period of the open state, after which the state of the
	// circuit breaker becomes half-open. If Timeout is 0, it will be set to 60 seconds.
	Timeout time.Duration `mapstructure:"timeout"`

	// ReadyToTrip is called with a copy of the Counts whenever a request fails
	// in the closed state. If ReadyToTrip returns true, the circuit breaker
	// will be placed into the open state. If ReadyToTrip is nil, default
	// ReadyToTrip is used. Default ReadyToTrip returns true when the number of
	// consecutive failures is more than 5.
	ReadyToTrip func(counts gobreaker.Counts) bool `mapstructure:"-"`

	// OnStateChange is called whenever the state of the circuit breaker changes.
	OnStateChange func(name string, from gobreaker.State, to gobreaker.State) `mapstructure:"-"`
}

type ext struct {
	cb *gobreaker.CircuitBreaker[any]
}

// circuitBreakerRoundTripper wraps an http.RoundTripper with circuit breaker logic
type circuitBreakerRoundTripper struct {
	rt  http.RoundTripper
	cb  *gobreaker.CircuitBreaker[any]
}

func (c *circuitBreakerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	result, err := c.cb.Execute(func() (any, error) {
		resp, err := c.rt.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		// Consider 5xx status codes as failures
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
		}
		return resp, nil
	})

	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return nil, fmt.Errorf("%w: %v", ErrCircuitBreakerOpen, err)
		}
		return nil, err
	}

	// Type assert the result back to *http.Response
	if resp, ok := result.(*http.Response); ok {
		return resp, nil
	}

	// This shouldn't happen, but handle it gracefully
	return nil, fmt.Errorf("unexpected circuit breaker result type")
}

// GetHTTPRoundTripper implements extensionmiddleware.HTTPClient.
func (e *ext) GetHTTPRoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	return &circuitBreakerRoundTripper{
		rt: base,
		cb: e.cb,
	}, nil
}

// GetGRPCClientOptions implements extensionmiddleware.GRPCClient.
func (e *ext) GetGRPCClientOptions() ([]grpc.DialOption, error) {
	return []grpc.DialOption{
		grpc.WithUnaryInterceptor(e.unaryInterceptor),
		grpc.WithStreamInterceptor(e.streamInterceptor),
	}, nil
}

// unaryInterceptor is a gRPC unary client interceptor that applies circuit breaker logic
func (e *ext) unaryInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	_, err := e.cb.Execute(func() (any, error) {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if err != nil {
			// Check if it's a gRPC error that should be considered a failure
			if st, ok := status.FromError(err); ok {
				// Consider server errors (5xx) and unavailable as failures
				if st.Code() == codes.Internal || st.Code() == codes.Unavailable ||
					st.Code() == codes.DeadlineExceeded || st.Code() == codes.ResourceExhausted {
					return nil, err
				}
			}
			return nil, err
		}
		return nil, nil
	})

	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return status.Error(codes.Unavailable, fmt.Sprintf("circuit breaker is open: %v", err))
		}
		return err
	}

	return nil
}

// streamInterceptor is a gRPC stream client interceptor that applies circuit breaker logic
func (e *ext) streamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	var stream grpc.ClientStream
	_, err := e.cb.Execute(func() (any, error) {
		var err error
		stream, err = streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			// Check if it's a gRPC error that should be considered a failure
			if st, ok := status.FromError(err); ok {
				// Consider server errors (5xx) and unavailable as failures
				if st.Code() == codes.Internal || st.Code() == codes.Unavailable ||
					st.Code() == codes.DeadlineExceeded || st.Code() == codes.ResourceExhausted {
					return nil, err
				}
			}
			return nil, err
		}
		return stream, nil
	})

	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return nil, status.Error(codes.Unavailable, fmt.Sprintf("circuit breaker is open: %v", err))
		}
		return nil, err
	}

	if stream == nil {
		return nil, status.Error(codes.Internal, "stream is nil")
	}

	return stream, nil
}

// Shutdown implements extension.Extension.
func (e *ext) Shutdown(ctx context.Context) error {
	return nil
}

// Start implements extension.Extension.
func (e *ext) Start(ctx context.Context, host component.Host) error {
	return nil
}

func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType("resilient"),
		createDefaultConfig,
		createExtension,
		component.StabilityLevelDevelopment,
	)
}

func createExtension(ctx context.Context, set extension.Settings, cfg component.Config) (extension.Extension, error) {
	config := cfg.(*Config)
	logger := set.Logger

	// Set defaults
	settings := gobreaker.Settings{
		Name: "resilient-circuit-breaker",
	}

	if config.MaxRequests > 0 {
		settings.MaxRequests = config.MaxRequests
	} else {
		settings.MaxRequests = 1
	}

	if config.Interval > 0 {
		settings.Interval = config.Interval
	} else {
		settings.Interval = 60 * time.Second
	}

	if config.Timeout > 0 {
		settings.Timeout = config.Timeout
	} else {
		settings.Timeout = 60 * time.Second
	}

	if config.ReadyToTrip != nil {
		settings.ReadyToTrip = config.ReadyToTrip
	} else {
		// Default: trip after 5 consecutive failures
		settings.ReadyToTrip = func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 5
		}
	}

	if config.OnStateChange != nil {
		settings.OnStateChange = config.OnStateChange
	} else {
		// Add default logging for state changes
		settings.OnStateChange = func(name string, from gobreaker.State, to gobreaker.State) {
			logger.Info("Circuit breaker state changed",
				zap.String("name", name),
				zap.String("from", from.String()),
				zap.String("to", to.String()),
			)
		}
	}

	cb := gobreaker.NewCircuitBreaker[any](settings)

	return &ext{
		cb: cb,
	}, nil
}

func createDefaultConfig() component.Config {
	return &Config{
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:    60 * time.Second,
	}
}
