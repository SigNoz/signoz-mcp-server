package otel

import (
	"context"
	"slices"
	"testing"

	"github.com/SigNoz/signoz-mcp-server/pkg/version"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

func TestMetricsIncludeBuildVersionResource(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.version=from-env")

	ctx := context.Background()
	res, err := NewResource(ctx, version.Version)
	if err != nil {
		t.Fatalf("NewResource() error = %v", err)
	}

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)
	t.Cleanup(func() { _ = provider.Shutdown(ctx) })

	meters, err := NewMeters(provider)
	if err != nil {
		t.Fatalf("NewMeters() error = %v", err)
	}
	meters.DocsSearchDuration.Record(ctx, .05)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if collected.Resource == nil {
		t.Fatal("collected metrics have no resource")
	}

	got, ok := collected.Resource.Set().Value(semconv.ServiceVersionKey)
	if !ok {
		t.Fatal("collected metrics resource has no service.version")
	}
	if got.AsString() != version.Version {
		t.Fatalf("service.version = %q, want %q", got.AsString(), version.Version)
	}
}

func TestDocsDurationHistogramsUseSecondScaleBuckets(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	meters, err := NewMeters(provider)
	if err != nil {
		t.Fatalf("NewMeters() error = %v", err)
	}

	meters.DocsSearchDuration.Record(context.Background(), .05)
	meters.DocsRefreshDuration.Record(context.Background(), 30)
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	wants := map[string][]float64{
		"signoz_docs_search_duration_seconds":  {.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		"signoz_docs_refresh_duration_seconds": {.1, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
	}
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			want, ok := wants[metric.Name]
			if !ok {
				continue
			}
			histogram, ok := metric.Data.(metricdata.Histogram[float64])
			if !ok || len(histogram.DataPoints) != 1 {
				t.Fatalf("%s data = %T with %d points, want one float64 histogram", metric.Name, metric.Data, len(histogram.DataPoints))
			}
			if !slices.Equal(histogram.DataPoints[0].Bounds, want) {
				t.Fatalf("%s bounds = %v, want %v", metric.Name, histogram.DataPoints[0].Bounds, want)
			}
			delete(wants, metric.Name)
		}
	}
	if len(wants) != 0 {
		t.Fatalf("duration histograms not collected: %v", wants)
	}
}
