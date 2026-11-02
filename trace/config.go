package trace

import (
	"math"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	defaultBatchTimeout    = 5 * time.Second
	defaultExportTimeout   = 10 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	defaultMaxQueueSize    = 2048
	defaultSampleRate      = 0.01
)

type TracerConfig struct {
	AppName         string
	AppVersion      string
	TracerVendor    string
	TraceURL        string
	TraceEnabled    bool
	BatchTimeout    time.Duration
	MaxBatchSize    int
	MaxQueueSize    int
	ExportTimeout   time.Duration
	ShutdownTimeout time.Duration
	Insecure        bool
	SampleRate      float64              // 0.0 to 1.0
	SampleRateSet   bool                 // Distinguishes an explicit zero from the default
	IDGenerator     sdktrace.IDGenerator // Optional; defaults to random trace and span IDs
	ExporterType    ExporterType         // GRPC or HTTP, default GRPC
}

// Validate checks if the configuration is valid.
func (c *TracerConfig) Validate() error {
	if c.AppName == "" {
		return ErrAppNameRequired
	}

	if c.TraceEnabled && c.TraceURL == "" {
		return ErrTraceURLRequired
	}

	if err := c.validateLimits(); err != nil {
		return err
	}

	if math.IsNaN(c.SampleRate) || c.SampleRate < 0.0 || c.SampleRate > 1.0 {
		return ErrInvalidSampleRate
	}

	return nil
}

func (c *TracerConfig) validateLimits() error {
	if c.BatchTimeout < 0 || c.ExportTimeout < 0 || c.ShutdownTimeout < 0 || c.MaxBatchSize < 0 || c.MaxQueueSize < 0 {
		return ErrInvalidLimits
	}

	queueSize := c.MaxQueueSize
	if queueSize == 0 {
		queueSize = defaultMaxQueueSize
	}

	batchSize := c.MaxBatchSize
	if batchSize == 0 {
		batchSize = 512
	}

	if batchSize > queueSize {
		return ErrInvalidLimits
	}

	return nil
}

// setDefaults sets default values for optional configuration fields.
func (c *TracerConfig) setDefaults() {
	if c.BatchTimeout == 0 {
		c.BatchTimeout = defaultBatchTimeout
	}

	if c.MaxBatchSize == 0 {
		c.MaxBatchSize = 512
	}

	if c.MaxQueueSize == 0 {
		c.MaxQueueSize = defaultMaxQueueSize
	}

	if c.ExportTimeout == 0 {
		c.ExportTimeout = defaultExportTimeout
	}

	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}

	if c.SampleRate == 0.0 && !c.SampleRateSet {
		c.SampleRate = defaultSampleRate
	}

	if c.ExporterType.IsZero() {
		exporterType, err := NewExporterType(ExporterTypeGRPC)
		if err == nil {
			c.ExporterType = exporterType
		}
	}
}
