package trace_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cristiano-pacheco/go-otel/trace"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

// lifecycleConfig uses a long batch delay so shutdown, not a timer, flushes spans.
func lifecycleConfig(t *testing.T, endpoint string) trace.TracerConfig {
	t.Helper()

	exporter, err := trace.NewExporterType(trace.ExporterTypeHTTP)
	if err != nil {
		t.Fatal(err)
	}

	return trace.TracerConfig{
		AppName:      "lifecycle-test",
		TraceEnabled: true,
		TraceURL:     endpoint,
		Insecure:     true,
		ExporterType: exporter,
		SampleRate:   1,
		BatchTimeout: time.Hour,
	}
}

func lifecycleCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if trace.IsInitialized() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			if err := trace.Shutdown(ctx); err != nil {
				t.Errorf("cleanup shutdown: %v", err)
			}
		}
	})
}

//nolint:paralleltest // The public API owns process-wide state.
func TestShutdownFlushesOTLPSpans(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []*collectortrace.ExportTraceServiceRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read export: %v", err)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		req := new(collectortrace.ExportTraceServiceRequest)
		if decodeErr := proto.Unmarshal(body, req); decodeErr != nil {
			t.Errorf("decode export: %v", decodeErr)
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		mu.Lock()

		requests = append(requests, req)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	lifecycleCleanup(t)

	for i := range 2 {
		if err := trace.Initialize(lifecycleConfig(t, strings.TrimPrefix(server.URL, "http://"))); err != nil {
			t.Fatalf("initialize cycle %d: %v", i, err)
		}

		_, span := trace.Span(context.Background(), "flush-me")
		span.End()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := trace.Shutdown(ctx)

		cancel()

		if err != nil {
			t.Fatalf("shutdown cycle %d: %v", i, err)
		}

		if trace.IsInitialized() {
			t.Fatal("tracer remained initialized after shutdown")
		}
	}

	mu.Lock()
	defer mu.Unlock()

	checkFlushedSpans(t, requests)
}

func checkFlushedSpans(t *testing.T, requests []*collectortrace.ExportTraceServiceRequest) {
	t.Helper()

	names := lifecycleSpanNames(requests)
	if len(names) != 2 || names[0] != "flush-me" || names[1] != "flush-me" {
		t.Fatalf("exported spans = %v, want two flush-me spans", names)
	}
}

func lifecycleSpanNames(requests []*collectortrace.ExportTraceServiceRequest) []string {
	var names []string

	for _, req := range requests {
		for _, resource := range req.GetResourceSpans() {
			for _, scope := range resource.GetScopeSpans() {
				for _, span := range scope.GetSpans() {
					names = append(names, span.GetName())
				}
			}
		}
	}

	return names
}

//nolint:paralleltest // The public API owns process-wide state.
func TestUnavailableOTLPBackendDoesNotBlockInitialization(t *testing.T) {
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatal(listenErr)
	}

	endpoint := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	lifecycleCleanup(t)

	start := time.Now()

	if err := trace.Initialize(lifecycleConfig(t, endpoint)); err != nil {
		t.Fatalf("initialize with unavailable backend: %v", err)
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("initialize took %v", elapsed)
	}

	_, span := trace.Span(context.Background(), "unavailable")
	span.End()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_ = trace.Shutdown(ctx) // Export failure is allowed; tracing must not prevent termination.

	if time.Since(start) > 2*time.Second {
		t.Fatal("shutdown exceeded its deadline by more than scheduling overhead")
	}

	if trace.IsInitialized() {
		t.Fatal("failed export left tracing initialized")
	}
}

//nolint:paralleltest // The public API owns process-wide state.
func TestShutdownDeadlineCleansUpForNextLifecycle(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}

		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)

	lifecycleCleanup(t)

	config := lifecycleConfig(t, strings.TrimPrefix(server.URL, "http://"))
	if err := trace.Initialize(config); err != nil {
		t.Fatal(err)
	}

	_, span := trace.Span(context.Background(), "blocked")
	span.End()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()

	shutdownErr := trace.Shutdown(ctx)
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", shutdownErr)
	}

	select {
	case <-started:
	default:
		t.Fatal("receiver did not receive the export before deadline")
	}

	if time.Since(start) > 2*time.Second {
		t.Fatal("shutdown did not honor deadline")
	}

	if trace.IsInitialized() {
		t.Fatal("deadline left tracing initialized")
	}

	if err := trace.Initialize(config); err != nil {
		t.Fatalf("reinitialize after failed shutdown: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()

	if err := trace.Shutdown(ctx2); err != nil {
		t.Fatalf("shutdown after reinitialization: %v", err)
	}
}

//nolint:paralleltest // The public API owns process-wide state.
func TestInvalidInitializationAndRepeatedShutdown(t *testing.T) {
	lifecycleCleanup(t)

	if err := trace.Initialize(trace.TracerConfig{}); !errors.Is(err, trace.ErrAppNameRequired) {
		t.Fatalf("invalid configuration error = %v", err)
	}

	if trace.IsInitialized() {
		t.Fatal("invalid initialization left tracing initialized")
	}

	if err := trace.Initialize(trace.TracerConfig{AppName: "valid"}); err != nil {
		t.Fatalf("initialize after invalid configuration: %v", err)
	}

	if err := trace.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	// Both an idempotent no-op and a documented not-initialized error are safe.
	if err := trace.Shutdown(context.Background()); err != nil && !errors.Is(err, trace.ErrNotInitialized) {
		t.Fatalf("repeated shutdown: %v", err)
	}

	if err := trace.Initialize(trace.TracerConfig{AppName: "valid-again"}); err != nil {
		t.Fatalf("initialize after repeated shutdown: %v", err)
	}
}

//nolint:paralleltest // The public API owns process-wide state.
func TestConfiguredShutdownBudget(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}

		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)

	lifecycleCleanup(t)
	config := lifecycleConfig(t, strings.TrimPrefix(server.URL, "http://"))

	config.ShutdownTimeout = 200 * time.Millisecond
	if err := trace.Initialize(config); err != nil {
		t.Fatal(err)
	}

	_, span := trace.Span(context.Background(), "budget")
	span.End()

	start := time.Now()

	err := trace.Shutdown(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}

	select {
	case <-started:
	default:
		t.Fatal("receiver did not receive export")
	}

	if time.Since(start) > 2*time.Second {
		t.Fatal("shutdown exceeded configured budget")
	}

	if trace.IsInitialized() {
		t.Fatal("timed-out shutdown left tracing initialized")
	}
}

//nolint:paralleltest // The public API owns process-wide state.
func TestConfiguredExportTimeout(t *testing.T) {
	canceled := make(chan struct{}, 1)
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
			canceled <- struct{}{}
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	lifecycleCleanup(t)
	config := lifecycleConfig(t, strings.TrimPrefix(server.URL, "http://"))
	config.BatchTimeout = 10 * time.Millisecond

	config.ExportTimeout = 100 * time.Millisecond
	if err := trace.Initialize(config); err != nil {
		t.Fatal(err)
	}

	_, span := trace.Span(context.Background(), "export-timeout")
	span.End()

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("export was not canceled within configured timeout")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := trace.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

//nolint:paralleltest // The public API owns process-wide state.
func TestShutdownBudgetCancelsInFlightExport(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)

		close(started)

		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	lifecycleCleanup(t)
	config := lifecycleConfig(t, strings.TrimPrefix(server.URL, "http://"))
	config.BatchTimeout = 10 * time.Millisecond
	config.ExportTimeout = 10 * time.Second

	config.ShutdownTimeout = 200 * time.Millisecond
	if err := trace.Initialize(config); err != nil {
		t.Fatal(err)
	}

	_, span := trace.Span(context.Background(), "in-flight")
	span.End()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("export did not start")
	}

	start := time.Now()

	err := trace.Shutdown(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}

	if time.Since(start) > 2*time.Second {
		t.Fatal("shutdown exceeded configured budget")
	}

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight export was not canceled by shutdown budget")
	}

	if trace.IsInitialized() {
		t.Fatal("timed-out shutdown left tracing initialized")
	}
}

//nolint:paralleltest // The public API owns process-wide state.
func TestAlreadyCanceledShutdownContextCleansUp(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)

		close(started)

		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	lifecycleCleanup(t)
	config := lifecycleConfig(t, strings.TrimPrefix(server.URL, "http://"))
	config.BatchTimeout = 10 * time.Millisecond
	config.ExportTimeout = 10 * time.Second

	config.ShutdownTimeout = 200 * time.Millisecond
	if err := trace.Initialize(config); err != nil {
		t.Fatal(err)
	}

	_, span := trace.Span(context.Background(), "canceled-shutdown")
	span.End()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("export did not start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := trace.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown error = %v, want context canceled", err)
	}

	if trace.IsInitialized() {
		t.Fatal("canceled shutdown left tracing initialized")
	}

	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled shutdown did not clean up in-flight export")
	}

	if err := trace.Initialize(trace.TracerConfig{AppName: "after-canceled-shutdown"}); err != nil {
		t.Fatalf("initialize after canceled shutdown: %v", err)
	}

	if err := trace.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown after reinitialization: %v", err)
	}
}

//nolint:paralleltest // The public API owns process-wide state.
func TestConcurrentShutdownWaitsForFlush(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	var releaseOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)

		close(started)
		<-release
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer releaseOnce.Do(func() { close(release) })

	lifecycleCleanup(t)
	config := lifecycleConfig(t, strings.TrimPrefix(server.URL, "http://"))

	config.ShutdownTimeout = 3 * time.Second
	if err := trace.Initialize(config); err != nil {
		t.Fatal(err)
	}

	_, span := trace.Span(context.Background(), "concurrent-shutdown")
	span.End()

	first := make(chan error, 1)
	go func() { first <- trace.Shutdown(context.Background()) }()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not begin flushing")
	}

	second := make(chan error, 1)
	go func() { second <- trace.Shutdown(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	if err := trace.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting shutdown error = %v, want deadline exceeded", err)
	}

	select {
	case err := <-second:
		t.Fatalf("concurrent shutdown returned before flush finished: %v", err)
	default:
	}

	releaseOnce.Do(func() { close(release) })

	for i, result := range []<-chan error{first, second} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("shutdown caller %d: %v", i, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("shutdown caller %d did not finish after flush", i)
		}
	}
}
