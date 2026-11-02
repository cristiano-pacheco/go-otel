package trace

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

//nolint:gochecknoglobals // The package exposes a process-wide tracing API.
var (
	globalTracer         oteltrace.Tracer
	globalTracerProvider *sdktrace.TracerProvider
	globalShutdown       *shutdownState
	globalMutex          sync.RWMutex
	initialized          bool
)

type shutdownState struct {
	done chan struct{}
	err  error
}

// Initialize configures the global tracer.
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

	tp, err := newTracerProvider(config, res)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCreateTracerProvider, err)
	}

	setupGlobalTracing(tp)

	globalTracer = tp.Tracer(config.AppName)
	globalTracerProvider = tp
	globalShutdown = nil
	initialized = true

	return nil
}

// MustInitialize initializes the global tracer and panics if it fails.
func MustInitialize(config TracerConfig) {
	if err := Initialize(config); err != nil {
		panic(fmt.Sprintf("failed to initialize tracer: %v", err))
	}
}

// createResource creates and configures the OpenTelemetry resource.
func createResource(config TracerConfig) *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(config.AppName),
		semconv.ServiceVersion(config.AppVersion),
	)
}

// setupGlobalTracing configures global OpenTelemetry settings.
func setupGlobalTracing(tp *sdktrace.TracerProvider) {
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(newPropagator())
}

// newPropagator creates a composite text map propagator.
func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// newTracerProvider creates a new tracer provider with the given configuration.
func newTracerProvider(
	config TracerConfig,
	res *resource.Resource,
) (*sdktrace.TracerProvider, error) {
	if !config.TraceEnabled {
		options := []sdktrace.TracerProviderOption{
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.NeverSample()),
		}
		if config.IDGenerator != nil {
			options = append(options, sdktrace.WithIDGenerator(config.IDGenerator))
		}

		return sdktrace.NewTracerProvider(options...), nil
	}

	exp, err := newExporter(config)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCreateExporter, err)
	}

	exportCtx, exportCancel := context.WithCancel(context.Background())
	boundedExporter := &lifecycleExporter{SpanExporter: exp, ctx: exportCtx}

	// Configure batch span processor options
	batchOptions := []sdktrace.BatchSpanProcessorOption{
		sdktrace.WithBatchTimeout(config.BatchTimeout),
		sdktrace.WithMaxExportBatchSize(config.MaxBatchSize),
		sdktrace.WithMaxQueueSize(config.MaxQueueSize),
		sdktrace.WithExportTimeout(config.ExportTimeout),
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRate))

	processor := &lifecycleProcessor{
		SpanProcessor: sdktrace.NewBatchSpanProcessor(boundedExporter, batchOptions...),
		timeout:       config.ShutdownTimeout,
		exportCancel:  exportCancel,
	}

	options := []sdktrace.TracerProviderOption{
		sdktrace.WithSpanProcessor(processor),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	}
	if config.IDGenerator != nil {
		options = append(options, sdktrace.WithIDGenerator(config.IDGenerator))
	}

	tp := sdktrace.NewTracerProvider(options...)

	return tp, nil
}

type lifecycleProcessor struct {
	sdktrace.SpanProcessor

	timeout      time.Duration
	exportCancel context.CancelFunc
}

func (p *lifecycleProcessor) Shutdown(_ context.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	defer p.exportCancel()

	return p.SpanProcessor.Shutdown(ctx)
}

// lifecycleExporter cancels SDK drain exports when the shutdown budget expires.
type lifecycleExporter struct {
	sdktrace.SpanExporter

	ctx context.Context
}

func (e *lifecycleExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}

	exportCtx, cancel := context.WithCancel(ctx)

	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	defer cancel()

	return e.SpanExporter.ExportSpans(exportCtx, spans)
}

// newExporter creates a new OTLP exporter (gRPC or HTTP based on config).
func newExporter(config TracerConfig) (sdktrace.SpanExporter, error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.ExportTimeout)
	defer cancel()

	if config.ExporterType.IsGRPC() {
		return newGRPCExporter(ctx, config)
	}

	if config.ExporterType.IsHTTP() {
		return newHTTPExporter(ctx, config)
	}

	return nil, fmt.Errorf("%w: %s", ErrInvalidExporterType, config.ExporterType.String())
}

// newGRPCExporter creates a new OTLP gRPC exporter.
func newGRPCExporter(ctx context.Context, config TracerConfig) (sdktrace.SpanExporter, error) {
	options := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(config.TraceURL),
		otlptracegrpc.WithTimeout(config.ExportTimeout),
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

// newHTTPExporter creates a new OTLP HTTP exporter.
func newHTTPExporter(ctx context.Context, config TracerConfig) (sdktrace.SpanExporter, error) {
	options := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(config.TraceURL),
		otlptracehttp.WithTimeout(config.ExportTimeout),
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

// Span starts a new span with the given name and options. Before initialization,
// it returns the original context and a no-op span that can be ended safely.
// When tracing is disabled, it creates a non-recording child span. In either
// case, ending the returned span does not end an existing parent span.
//
//nolint:spancheck // The caller owns the returned span.
func Span(
	ctx context.Context,
	name string,
	opts ...oteltrace.SpanStartOption,
) (context.Context, oteltrace.Span) {
	globalMutex.RLock()
	defer globalMutex.RUnlock()

	if !initialized || globalTracer == nil {
		_, span := noop.NewTracerProvider().Tracer("").Start(ctx, name, opts...)
		return ctx, span
	}

	//nolint:spancheck // span is returned to caller who is responsible for ending it
	return globalTracer.Start(ctx, name, opts...)
}

// Shutdown flushes and shuts down the provider within the configured budget
// and the caller's deadline. Repeated calls after shutdown are safe.
func Shutdown(ctx context.Context) error {
	globalMutex.Lock()
	if !initialized {
		shutdown := globalShutdown
		globalMutex.Unlock()

		if shutdown == nil {
			return nil
		}

		return waitForShutdown(ctx, shutdown)
	}

	shutdown := &shutdownState{done: make(chan struct{})}
	globalShutdown = shutdown
	provider := globalTracerProvider
	globalTracer = nil
	globalTracerProvider = nil
	initialized = false

	otel.SetTracerProvider(noop.NewTracerProvider())
	globalMutex.Unlock()

	// The SDK skips processor cleanup if Shutdown starts with a canceled context.
	// Give cleanup its own budget even when the caller has already timed out.
	//nolint:gosec // Cleanup must run even if the caller's context is already canceled.
	go func() {
		shutdown.err = provider.Shutdown(context.Background())
		close(shutdown.done)
	}()

	return waitForShutdown(ctx, shutdown)
}

func waitForShutdown(ctx context.Context, shutdown *shutdownState) error {
	select {
	case <-shutdown.done:
		if shutdown.err != nil {
			return fmt.Errorf("%w: %w", ErrTracerProviderShutdown, shutdown.err)
		}

		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrTracerProviderShutdown, ctx.Err())
	}
}

// IsInitialized returns true if the tracer has been initialized.
func IsInitialized() bool {
	globalMutex.RLock()
	defer globalMutex.RUnlock()

	return initialized
}
