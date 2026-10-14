package oteltest

import "go.opentelemetry.io/otel/sdk/metric/metricdata"

func FindInt64SumMetric(rm metricdata.ResourceMetrics, name string) (metricdata.Sum[int64], bool) {
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			if metric.Name != name {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if ok {
				return sum, true
			}
		}
	}
	return metricdata.Sum[int64]{}, false
}

func FindFloat64HistogramMetric(rm metricdata.ResourceMetrics, name string) (metricdata.Histogram[float64], bool) {
	for _, scopeMetric := range rm.ScopeMetrics {
		for _, metric := range scopeMetric.Metrics {
			if metric.Name != name {
				continue
			}
			histogram, ok := metric.Data.(metricdata.Histogram[float64])
			if ok {
				return histogram, true
			}
		}
	}
	return metricdata.Histogram[float64]{}, false
}
