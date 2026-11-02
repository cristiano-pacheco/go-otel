package trace_test

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cristiano-pacheco/go-otel/trace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

//nolint:paralleltest // The tracing API owns process-wide state.
func TestSpanOwnership(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))

	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("parent provider shutdown: %v", err)
		}
	})

	for _, mode := range []string{"uninitialized", "disabled", "enabled"} {
		t.Run(mode, func(t *testing.T) {
			initializeForSpanTest(t, mode)
			checkSpanOwnership(t, provider, mode)
		})
	}
}

func initializeForSpanTest(t *testing.T, mode string) {
	t.Helper()

	if mode == "uninitialized" {
		return
	}

	config := trace.TracerConfig{AppName: "test"}

	if mode == "enabled" {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server.Close)

		exporterType, err := trace.NewExporterType(trace.ExporterTypeHTTP)
		if err != nil {
			t.Fatal(err)
		}

		config.TraceEnabled = true
		config.TraceURL = strings.TrimPrefix(server.URL, "http://")
		config.Insecure = true
		config.ExporterType = exporterType
		config.SampleRate = 1
	}

	if err := trace.Initialize(config); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := trace.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
}

//nolint:gocognit // Exercise both parent contexts across all tracing modes.
func checkSpanOwnership(t *testing.T, provider *sdktrace.TracerProvider, mode string) {
	t.Helper()

	for _, withParent := range []bool{false, true} {
		ctx := context.Background()

		parent := oteltrace.SpanFromContext(ctx)
		if withParent {
			ctx, parent = provider.Tracer("parent").Start(ctx, "parent")
		}

		childCtx, child := trace.Span(ctx, "child")
		if mode == "uninitialized" && childCtx != ctx {
			t.Error("uninitialized span changed the context")
		}

		if mode == "enabled" && withParent &&
			(child.SpanContext().TraceID() != parent.SpanContext().TraceID() || child.SpanContext().SpanID() == parent.SpanContext().SpanID()) {
			t.Error("initialized span is not a child of the parent")
		}

		child.End()

		if !withParent {
			continue
		}

		if !parent.IsRecording() {
			t.Error("ending child ended the parent")
		}

		parent.End()
	}
}

func TestSampleRateValidation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		rate  float64
		valid bool
	}{
		{"zero", 0, true},
		{"fraction", 0.5, true},
		{"one", 1, true},
		{"negative", -0.01, false},
		{"above one", 1.01, false},
		{"NaN", math.NaN(), false},
		{"positive infinity", math.Inf(1), false},
		{"negative infinity", math.Inf(-1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			config := trace.TracerConfig{AppName: "test", SampleRate: test.rate}

			err := config.Validate()
			if test.valid && err != nil {
				t.Fatalf("valid rate rejected: %v", err)
			}

			if !test.valid && !errors.Is(err, trace.ErrInvalidSampleRate) {
				t.Fatalf("invalid rate error = %v", err)
			}
		})
	}
}

//nolint:paralleltest // The tracing API owns process-wide state.
func TestConfiguredSampling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exporterType, exporterErr := trace.NewExporterType(trace.ExporterTypeHTTP)
	if exporterErr != nil {
		t.Fatal(exporterErr)
	}

	//nolint:paralleltest // Each case initializes the same global tracer.
	for _, test := range []struct {
		name          string
		rate          float64
		set           bool
		effectiveRate float64
	}{
		{"omitted defaults to one percent", 0, false, 0.01},
		{"explicit zero", 0, true, 0},
		{"fraction", 0.5, false, 0.5},
		{"full", 1, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			generator := &controlledIDGenerator{}

			config := trace.TracerConfig{
				IDGenerator:   generator,
				AppName:       "test",
				TraceEnabled:  true,
				TraceURL:      strings.TrimPrefix(server.URL, "http://"),
				Insecure:      true,
				ExporterType:  exporterType,
				SampleRate:    test.rate,
				SampleRateSet: test.set,
			}
			if err := trace.Initialize(config); err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				if err := trace.Shutdown(context.Background()); err != nil {
					t.Errorf("shutdown: %v", err)
				}
			})

			checkRootSampling(t, test.effectiveRate, generator)
			checkParentSampling(t)
		})
	}
}

//nolint:paralleltest // The tracing API owns process-wide state.
func TestDisabledTracingIDGenerator(t *testing.T) {
	generator := &controlledIDGenerator{value: 42}
	if err := trace.Initialize(trace.TracerConfig{AppName: "test", IDGenerator: generator}); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := trace.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})

	_, span := trace.Span(context.Background(), "root")
	defer span.End()

	traceID := span.SpanContext().TraceID()
	if got := binary.BigEndian.Uint64(traceID[8:]) >> 1; got != generator.value {
		t.Errorf("trace ID value = %d, want %d", got, generator.value)
	}
}

type controlledIDGenerator struct {
	value uint64
}

func (g *controlledIDGenerator) NewIDs(context.Context) (oteltrace.TraceID, oteltrace.SpanID) {
	traceID := oteltrace.TraceID{1}
	binary.BigEndian.PutUint64(traceID[8:], g.value<<1)

	return traceID, oteltrace.SpanID{1}
}

func (g *controlledIDGenerator) NewSpanID(context.Context, oteltrace.TraceID) oteltrace.SpanID {
	return oteltrace.SpanID{1}
}

func checkRootSampling(t *testing.T, rate float64, generator *controlledIDGenerator) {
	t.Helper()

	values := []uint64{0, (1 << 63) - 1}

	if rate > 0 && rate < 1 {
		boundary := uint64(rate * (1 << 63))
		values = append(values, boundary-1, boundary)
	}

	for _, value := range values {
		generator.value = value
		_, span := trace.Span(context.Background(), "root")

		want := rate == 1 || (rate > 0 && value < uint64(rate*(1<<63)))
		if span.SpanContext().IsSampled() != want {
			t.Errorf("rate %v, value %d: sampled = %v, want %v", rate, value, span.SpanContext().IsSampled(), want)
		}

		span.End()
	}
}

func checkParentSampling(t *testing.T) {
	t.Helper()

	for _, sampled := range []bool{false, true} {
		for _, remote := range []bool{false, true} {
			flags := oteltrace.TraceFlags(0)
			if sampled {
				flags = oteltrace.FlagsSampled
			}

			parent := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
				TraceID: oteltrace.TraceID{1}, SpanID: oteltrace.SpanID{1}, TraceFlags: flags, Remote: remote,
			})
			ctx := oteltrace.ContextWithSpanContext(context.Background(), parent)

			_, span := trace.Span(ctx, "child")
			if span.SpanContext().IsSampled() != sampled {
				t.Errorf(
					"parent sampled=%v remote=%v: child sampled=%v",
					sampled,
					remote,
					span.SpanContext().IsSampled(),
				)
			}

			span.End()
		}
	}
}
