package trace

import "errors"

var (
	ErrAppNameRequired     = errors.New("AppName is required")
	ErrTraceURLRequired    = errors.New("TraceURL is required when tracing is enabled")
	ErrInvalidSampleRate   = errors.New("SampleRate must be between 0.0 and 1.0")
	ErrInvalidExporterType = errors.New("invalid exporter type (must be 'grpc' or 'http')")

	ErrAlreadyInitialized   = errors.New("tracer already initialized")
	ErrNotInitialized       = errors.New("tracer not initialized")
	ErrCreateTracerProvider = errors.New("failed to create tracer provider")
	ErrCreateExporter       = errors.New("failed to create exporter")

	ErrCreateGRPCExporter = errors.New("failed to create OTLP gRPC exporter")
	ErrCreateHTTPExporter = errors.New("failed to create OTLP HTTP exporter")

	ErrTracerProviderShutdown = errors.New("tracer provider shutdown failed")
	ErrExporterShutdown       = errors.New("exporter shutdown failed")
	ErrMultipleShutdown       = errors.New("multiple shutdown failures")
)
