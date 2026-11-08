package metrics

import (
	"context"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// NewProvider builds a MeterProvider whose reader and exporter come from the
// standard OTEL_* environment variables.
func NewProvider(ctx context.Context, version string) (*sdkmetric.MeterProvider, error) {
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName("omada-exporter"),
		semconv.ServiceVersion(version),
	))
	if err != nil {
		return nil, err
	}
	return sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithResource(res)), nil
}
