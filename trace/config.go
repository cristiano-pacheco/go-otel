package trace

import (
	"time"
)

const (
	defaultBatchTimeout = 5 * time.Second
	defaultSampleRate   = 0.01
)

type TracerConfig struct {
	AppName      string
	AppVersion   string
	TracerVendor string
	TraceURL     string
	TraceEnabled bool
	BatchTimeout time.Duration
	MaxBatchSize int
	Insecure     bool
	SampleRate   float64      // 0.0 to 1.0
	ExporterType ExporterType // GRPC or HTTP, default GRPC
}

// Validate checks if the configuration is valid
func (c *TracerConfig) Validate() error {
	if c.AppName == "" {
		return ErrAppNameRequired
	}
	if c.TraceEnabled && c.TraceURL == "" {
		return ErrTraceURLRequired
	}
	if c.SampleRate < 0.0 || c.SampleRate > 1.0 {
		return ErrInvalidSampleRate
	}
	return nil
}

// setDefaults sets default values for optional configuration fields
func (c *TracerConfig) setDefaults() {
	if c.BatchTimeout == 0 {
		c.BatchTimeout = defaultBatchTimeout
	}
	if c.MaxBatchSize == 0 {
		c.MaxBatchSize = 512
	}
	if c.SampleRate == 0.0 {
		c.SampleRate = defaultSampleRate
	}
	if c.ExporterType.IsZero() {
		exporterType, err := NewExporterType(ExporterTypeGRPC)
		if err == nil {
			c.ExporterType = exporterType
		}
	}
}
