package main

import (
	"context"
	"log"
	"time"

	"github.com/cristiano-pacheco/go-otel/trace"
	"go.opentelemetry.io/otel/attribute"
)

func main() {
	// Initialize tracer
	config := trace.TracerConfig{
		AppName:      "example-app",
		AppVersion:   "1.0.0",
		TraceURL:     "localhost:4318",
		TraceEnabled: false, // Set to true to send traces
		Insecure:     true,
		SampleRate:   1.0,
	}

	trace.MustInitialize(config)
	defer trace.Shutdown(context.Background())

	log.Println("Tracer initialized")

	// Use tracer anywhere without injection
	ctx := context.Background()
	processOrder(ctx, "order-123")

	log.Println("Done")
}

func processOrder(ctx context.Context, orderID string) {
	ctx, span := trace.StartSpan(ctx, "process-order")
	defer span.End()

	span.SetAttributes(attribute.String("order.id", orderID))

	// Simulate work
	fetchData(ctx)
	saveData(ctx)

	log.Printf("Order %s processed", orderID)
}

func fetchData(ctx context.Context) {
	ctx, span := trace.StartSpan(ctx, "fetch-data")
	defer span.End()

	time.Sleep(50 * time.Millisecond)
	log.Println("Data fetched")
}

func saveData(ctx context.Context) {
	ctx, span := trace.StartSpan(ctx, "save-data")
	defer span.End()

	span.SetAttributes(attribute.String("db.system", "postgres"))
	time.Sleep(30 * time.Millisecond)
	log.Println("Data saved")
}
