package main

import (
	"context"
	"log"
	"time"

	"github.com/cristiano-pacheco/go-otel/trace"
	"go.opentelemetry.io/otel/attribute"
)

const (
	simulatedFetchDuration = 50 * time.Millisecond
	simulatedSaveDuration  = 30 * time.Millisecond
)

func main() {
	// Initialize tracer
	exporterType, err := trace.NewExporterType(trace.ExporterTypeGRPC) // or trace.ExporterTypeHTTP
	if err != nil {
		log.Fatalf("Invalid exporter type: %v", err)
	}
	config := trace.TracerConfig{
		AppName:      "example-app",
		AppVersion:   "1.0.0",
		TraceURL:     "localhost:4318",
		TraceEnabled: false, // Set to true to send traces
		Insecure:     true,
		SampleRate:   1.0,
		ExporterType: exporterType,
	}

	trace.MustInitialize(config)
	defer func() {
		if shutdownErr := trace.Shutdown(context.Background()); shutdownErr != nil {
			log.Printf("Error shutting down tracer: %v", shutdownErr)
		}
	}()

	log.Println("Tracer initialized")

	// Use tracer anywhere without injection
	ctx := context.Background()
	processOrder(ctx, "order-123")

	log.Println("Done")
}

func processOrder(ctx context.Context, orderID string) {
	ctx, span := trace.Span(ctx, "process-order")
	defer span.End()

	span.SetAttributes(attribute.String("order.id", orderID))

	// Simulate work
	fetchData(ctx)
	saveData(ctx)

	log.Printf("Order %s processed", orderID)
}

func fetchData(ctx context.Context) {
	_, span := trace.Span(ctx, "fetch-data")
	defer span.End()

	time.Sleep(simulatedFetchDuration)
	log.Println("Data fetched")
}

func saveData(ctx context.Context) {
	_, span := trace.Span(ctx, "save-data")
	defer span.End()

	span.SetAttributes(attribute.String("db.system", "postgres"))
	time.Sleep(simulatedSaveDuration)
	log.Println("Data saved")
}
