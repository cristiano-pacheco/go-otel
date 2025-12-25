package trace

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"
	oteltrace "go.opentelemetry.io/otel/trace"
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
		return ErrAlreadyInitialized
	}

	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	config.setDefaults()

	res := createResource(config)

	tp, exp, err := newTracerProvider(config, res)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCreateTracerProvider, err)
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
		return nil, nil, fmt.Errorf("%w: %w", ErrCreateExporter, err)
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

// newExporter creates a new OTLP exporter (gRPC or HTTP based on config)
func newExporter(config TracerConfig) (sdktrace.SpanExporter, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultBatchTimeout)
	defer cancel()

	if config.ExporterType.IsGRPC() {
		return newGRPCExporter(ctx, config)
	}

	if config.ExporterType.IsHTTP() {
		return newHTTPExporter(ctx, config)
	}

	return nil, fmt.Errorf("%w: %s", ErrInvalidExporterType, config.ExporterType.String())
}

// newGRPCExporter creates a new OTLP gRPC exporter
func newGRPCExporter(ctx context.Context, config TracerConfig) (sdktrace.SpanExporter, error) {
	options := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(config.TraceURL),
	}

	if config.Insecure {
		options = append(options, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreateGRPCExporter, err)
	}

	return exporter, nil
}

// newHTTPExporter creates a new OTLP HTTP exporter
func newHTTPExporter(ctx context.Context, config TracerConfig) (sdktrace.SpanExporter, error) {
	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(config.TraceURL),
	}

	if config.Insecure {
		options = append(options, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreateHTTPExporter, err)
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
		return ErrNotInitialized
	}

	logger := slog.Default()
	var shutdownErr error

	if globalTracerProvider != nil {
		if err := globalTracerProvider.Shutdown(ctx); err != nil {
			logger.ErrorContext(ctx, "Failed to shutdown tracer provider", "error", err)
			shutdownErr = fmt.Errorf("%w: %w", ErrTracerProviderShutdown, err)
		} else {
			logger.InfoContext(ctx, "Tracer provider shutdown successfully...")
		}
	}

	if globalExporter != nil {
		if err := globalExporter.Shutdown(ctx); err != nil {
			logger.ErrorContext(ctx, "Failed to shutdown exporter", "error", err)
			if shutdownErr != nil {
				return fmt.Errorf("%w - tracer: %w, exporter: %w", ErrMultipleShutdown, shutdownErr, err)
			}
			return fmt.Errorf("%w: %w", ErrExporterShutdown, err)
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
