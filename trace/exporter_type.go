package trace

import "fmt"

const (
	ExporterTypeGRPC = "grpc"
	ExporterTypeHTTP = "http"
)

type ExporterType struct {
	value string
}

func NewExporterType(value string) (ExporterType, error) {
	switch value {
	case ExporterTypeGRPC, ExporterTypeHTTP:
		return ExporterType{value: value}, nil
	default:
		return ExporterType{}, fmt.Errorf("%w: %s", ErrInvalidExporterType, value)
	}
}

func (e ExporterType) String() string {
	return e.value
}

func (e ExporterType) IsGRPC() bool {
	return e.value == ExporterTypeGRPC
}

func (e ExporterType) IsHTTP() bool {
	return e.value == ExporterTypeHTTP
}

func (e ExporterType) IsZero() bool {
	return e.value == ""
}
