package trace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	defaultBatchTimeout = 5 * time.Second
	defaultSampleRate   = 0.01
)

var (
	globalTracer         oteltrace.Tracer
	globalTracerProvider *sdktrace.TracerProvider
	globalExporter       sdktrace.SpanExporter
	globalMutex          sync.RWMutex
	initialized          bool
)

// Initialize configures the global tracer. Must be called before using StartSpan.
// Returns an error if initialization fails.
func Initialize(config TracerConfig) error {
	globalMutex.Lock()
	defer globalMutex.Unlock()

	if initialized {
		return errors.New("tracer already initialized")
	}

	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	config.setDefaults()

	res := createResource(config)

	tp, exp, err := newTracerProvider(config, res)
	if err != nil {
		return fmt.Errorf("failed to create tracer provider: %w", err)
	}

	setupGlobalTracing(tp)

	globalTracer = tp.Tracer(config.AppName)
	globalTracerProvider = tp
	globalExporter = exp
	initialized = true

	return nil
}

// MustInitialize initializes the global tracer and panics if it fails.
func MustInitialize(config TracerConfig) {
	if err := Initialize(config); err != nil {
		panic(fmt.Sprintf("failed to initialize tracer: %v", err))
	}
}

// createResource creates and configures the OpenTelemetry resource
func createResource(config TracerConfig) *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(config.AppName),
		semconv.ServiceVersion(config.AppVersion),
	)
}

// setupGlobalTracing configures global OpenTelemetry settings
func setupGlobalTracing(tp *sdktrace.TracerProvider) {
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(newPropagator())
}

// newPropagator creates a composite text map propagator
func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// newTracerProvider creates a new tracer provider with the given configuration
func newTracerProvider(
	config TracerConfig,
	res *resource.Resource,
) (*sdktrace.TracerProvider, sdktrace.SpanExporter, error) {
	if !config.TraceEnabled {
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.NeverSample()),
		)
		return tp, nil, nil
	}

	exp, err := newExporter(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create exporter: %w", err)
	}

	// Configure batch span processor options
	batchOptions := []sdktrace.BatchSpanProcessorOption{
		sdktrace.WithBatchTimeout(config.BatchTimeout),
		sdktrace.WithMaxExportBatchSize(config.MaxBatchSize),
	}

	// Configure sampling
	sampler := sdktrace.TraceIDRatioBased(config.SampleRate)
	if config.SampleRate >= defaultSampleRate {
		sampler = sdktrace.AlwaysSample()
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(exp, batchOptions...)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	return tp, exp, nil
}

// newExporter creates a new OTLP HTTP exporter
func newExporter(config TracerConfig) (sdktrace.SpanExporter, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultBatchTimeout)
	defer cancel()

	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(config.TraceURL),
	}

	if config.Insecure {
		options = append(options, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP HTTP exporter: %w", err)
	}

	return exporter, nil
}

// StartSpan starts a new span with the given name.
// The tracer must be initialized first by calling Initialize or MustInitialize.
func StartSpan(ctx context.Context, name string) (context.Context, oteltrace.Span) {
	globalMutex.RLock()
	defer globalMutex.RUnlock()

	if !initialized || globalTracer == nil {
		// Return a no-op span if not initialized
		return ctx, oteltrace.SpanFromContext(ctx)
	}

	//nolint:spancheck // span is returned to caller who is responsible for ending it
	return globalTracer.Start(ctx, name)
}

// StartSpanWithOptions starts a new span with custom options.
func StartSpanWithOptions(
	ctx context.Context,
	name string,
	opts ...oteltrace.SpanStartOption,
) (context.Context, oteltrace.Span) {
	globalMutex.RLock()
	defer globalMutex.RUnlock()

	if !initialized || globalTracer == nil {
		// Return a no-op span if not initialized
		return ctx, oteltrace.SpanFromContext(ctx)
	}

	//nolint:spancheck // span is returned to caller who is responsible for ending it
	return globalTracer.Start(ctx, name, opts...)
}

// Shutdown gracefully shuts down the tracer provider and exporter.
// Should be called during application shutdown.
func Shutdown(ctx context.Context) error {
	globalMutex.Lock()
	defer globalMutex.Unlock()

	if !initialized {
		return errors.New("tracer not initialized")
	}

	logger := slog.Default()
	var shutdownErr error

	if globalTracerProvider != nil {
		if err := globalTracerProvider.Shutdown(ctx); err != nil {
			logger.ErrorContext(ctx, "Failed to shutdown tracer provider", "error", err)
			shutdownErr = fmt.Errorf("tracer provider shutdown failed: %w", err)
		} else {
			logger.InfoContext(ctx, "Tracer provider shutdown successfully...")
		}
	}

	if globalExporter != nil {
		if err := globalExporter.Shutdown(ctx); err != nil {
			logger.ErrorContext(ctx, "Failed to shutdown exporter", "error", err)
			if shutdownErr != nil {
				return fmt.Errorf("multiple shutdown failures - tracer: %w, exporter: %w", shutdownErr, err)
			}
			return fmt.Errorf("exporter shutdown failed: %w", err)
		}
		logger.InfoContext(ctx, "Exporter shutdown successfully...")
	}

	// Reset global state
	globalTracer = nil
	globalTracerProvider = nil
	globalExporter = nil
	initialized = false

	return shutdownErr
}

// IsInitialized returns true if the tracer has been initialized.
func IsInitialized() bool {
	globalMutex.RLock()
	defer globalMutex.RUnlock()
	return initialized
}
