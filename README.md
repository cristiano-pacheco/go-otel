# Trace Package

Package for distributed tracing using OpenTelemetry, with a global function-based API (no need for dependency injection).

## Features

- ✅ **No instances**: Use `trace.StartSpan()` directly without dependency injection
- ✅ **Thread-safe**: Can be used concurrently in goroutines
- ✅ **One-time initialization**: Configure once at application startup
- ✅ **Flexible**: Supports OTLP HTTP, configurable sampling, and more
- ✅ **Graceful shutdown**: Safe shutdown with timeout

## Installation

```bash
go get github.com/cristiano-pacheco/go-otel@latest
```

## Basic Usage

### 1. Initialization (once in the application)

```go
import "github.com/cristiano-pacheco/go-otel/trace"

func main() {
    config := trace.TracerConfig{
        AppName:      "my-service",
        AppVersion:   "1.0.0",
        TracerVendor: "otlp",
        TraceURL:     "localhost:4318",
        TraceEnabled: true,
        Insecure:     true,
        SampleRate:   1.0, // 100% sampling
    }

    // Option 1: With error handling
    if err := trace.Initialize(config); err != nil {
        log.Fatal(err)
    }

    // Option 2: Panic on failure
    // trace.MustInitialize(config)

    defer func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        if err := trace.Shutdown(ctx); err != nil {
            log.Printf("Shutdown error: %v", err)
        }
    }()

    // Your application...
}
```

### 2. Creating Spans (anywhere in the code)

```go
func ProcessOrder(ctx context.Context, orderID string) error {
    // No need to inject tracer!
    ctx, span := trace.Span(ctx, "process-order")
    defer span.End()

    // Add attributes
    span.SetAttributes(
        attribute.String("order.id", orderID),
    )

    // Call other functions passing context
    if err := validateOrder(ctx, orderID); err != nil {
        span.RecordError(err)
        return err
    }

    return saveOrder(ctx, orderID)
}

func validateOrder(ctx context.Context, orderID string) error {
    // Nested span automatically
    ctx, span := trace.Span(ctx, "validate-order")
    defer span.End()

    // Your logic...
    return nil
}
```

### 3. Spans with Options

```go
import oteltrace "go.opentelemetry.io/otel/trace"

func QueryDatabase(ctx context.Context, query string) error {
    ctx, span := trace.Span(
        ctx,
        "db-query",
        oteltrace.WithSpanKind(oteltrace.SpanKindClient),
        oteltrace.WithAttributes(
            attribute.String("db.query", query),
        ),
    )
    defer span.End()

    // Execute query...
    return nil
}
```

## Configuration

```go
type TracerConfig struct {
    AppName      string        // Application name (required)
    AppVersion   string        // Application version
    TracerVendor string        // Tracer vendor (e.g., "otlp")
    TraceURL     string        // Collector URL (required if TraceEnabled=true)
    TraceEnabled bool          // Enable/disable tracing
    BatchTimeout time.Duration // Batch send timeout (default: 5s)
    MaxBatchSize int           // Maximum batch size (default: 512)
    Insecure     bool          // Use insecure connection (HTTP)
    SampleRate   float64       // Sample rate 0.0 to 1.0 (default: 0.01)
    ExporterType ExporterType  // GRPC or HTTP, default GRPC
}
```

### Configuration Examples

#### Development (without exporter)
```go
config := trace.TracerConfig{
    AppName:      "my-app",
    AppVersion:   "dev",
    TraceEnabled: false, // Doesn't send traces
    SampleRate:   1.0,
}
```

#### Production (with Jaeger/OTLP)
```go
config := trace.TracerConfig{
    AppName:      "my-app",
    AppVersion:   "1.0.0",
    TracerVendor: "otlp",
    TraceURL:     "jaeger-collector:4318",
    TraceEnabled: true,
    Insecure:     false,
    BatchTimeout: 5 * time.Second,
    MaxBatchSize: 512,
    SampleRate:   0.1, // 10% sampling to reduce volume
}
```

## API

### Main Functions

#### `Initialize(config TracerConfig) error`
Initializes the global tracer. Returns an error if configuration is invalid or if already initialized.

#### `MustInitialize(config TracerConfig)`
Version that panics if initialization fails.

#### `Span(ctx context.Context, name string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span)`
Starts a new span with optional configuration. Returns updated context and span. This unified method replaces both `StartSpan` and `StartSpanWithOptions`.

#### `Shutdown(ctx context.Context) error`
Safely shuts down the tracer, finishing sending pending spans.

#### `IsInitialized() bool`
Checks if the tracer has been initialized.

## Recommended Patterns

### ✅ Best Practices

```go
// 1. Always use defer to ensure span is finished
ctx, span := trace.Span(ctx, "operation")
defer span.End()

// 2. Pass context to called functions
result := processData(ctx, data)

// 3. Record errors in span
if err != nil {
    span.RecordError(err)
    span.SetStatus(codes.Error, err.Error())
    return err
}

// 4. Add meaningful attributes
span.SetAttributes(
    attribute.String("user.id", userID),
    attribute.Int("items.count", len(items)),
)
```

### ❌ Avoid

```go
// Don't forget to finish spans
ctx, span := trace.StartSpan(ctx, "operation")
// span.End() <- MISSING!

// Don't create spans without using returned context
_, span := trace.StartSpan(ctx, "operation") // ctx not updated!
defer span.End()

// Don't initialize multiple times
trace.Initialize(config) // OK
trace.Initialize(config) // ERROR!
```

## HTTP Integration

### Server Middleware

```go
func TracingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx, span := trace.StartSpan(r.Context(), r.URL.Path)
        defer span.End()

        span.SetAttributes(
            attribute.String("http.method", r.Method),
            attribute.String("http.url", r.URL.String()),
        )

        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

### HTTP Client

```go
func DoRequest(ctx context.Context, url string) error {
    ctx, span := trace.StartSpanWithOptions(
        ctx,
        "http-request",
        oteltrace.WithSpanKind(oteltrace.SpanKindClient),
    )
    defer span.End()

    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        span.RecordError(err)
        return err
    }

    // Propagate context via headers
    otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        span.RecordError(err)
        return err
    }
    defer resp.Body.Close()

    span.SetAttributes(
        attribute.Int("http.status_code", resp.StatusCode),
    )

    return nil
}
```

## Advantages of This Approach

1. **Simplicity**: No need to inject `Trace` in all constructors
2. **Less Boilerplate**: Reduces setup and DI code
3. **Easy Migration**: Add tracing to existing code without massive refactoring
4. **Performance**: Direct access without interface indirection
5. **Convenience**: Similar to logging - `log.Printf()` vs injecting logger

## Considerations

- The tracer uses a mutex to protect global state, but the impact is minimal
- Spans created before initialization return no-op spans (don't cause errors)
- In tests, you can call `Shutdown()` and `Initialize()` again between tests
- For environments with multiple tracers, consider using namespaces or separate packages

## Testing

```go
func TestMyFunction(t *testing.T) {
    // Setup
    config := trace.TracerConfig{
        AppName:      "test-app",
        TraceEnabled: false,
        SampleRate:   1.0,
    }
    trace.MustInitialize(config)
    defer trace.Shutdown(context.Background())

    // Test
    ctx := context.Background()
    ctx, span := trace.StartSpan(ctx, "test-operation")
    defer span.End()

    // Your assertions...
}
```

## License

MIT
