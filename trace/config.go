package trace

import (
	"errors"
	"time"
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
	SampleRate   float64 // 0.0 to 1.0
}

// Validate checks if the configuration is valid
func (c *TracerConfig) Validate() error {
	if c.AppName == "" {
		return errors.New("AppName is required")
	}
	if c.TraceEnabled && c.TraceURL == "" {
		return errors.New("TraceURL is required when tracing is enabled")
	}
	if c.SampleRate < 0.0 || c.SampleRate > 1.0 {
		return errors.New("SampleRate must be between 0.0 and 1.0")
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
		c.SampleRate = 1.0 // Default to sampling all traces
	}
}
