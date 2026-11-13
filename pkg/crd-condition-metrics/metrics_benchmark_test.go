package crd_condition_metrics

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

/*
Run:
	go test -run '^$' -bench . -benchtime=10000x -benchmem ./pkg/operator_condition_metrics
*/

// Let's benchmark against a somewhat realistic high usage scenario
const (
	controllerCount         = 10
	resourcesPerController  = 200
	conditionsPerController = 3
	variantsPerCondition    = 10
	// Maximum total time series variants: 10 * 200 * 3 * 10 = 60k
	// In our configuration however, we expect only one variant per condition to be exported.
	// Maximum total exported time series: 10 * 200 * 3 * 1 = 6k
	maxCardinality = controllerCount * resourcesPerController * conditionsPerController * variantsPerCondition
)

func generatedName(prefix string, i int) string {
	return fmt.Sprintf("%s%d", prefix, i)
}

type FakeObject struct {
	Name      string
	Namespace string
}

func (f *FakeObject) GetName() string {
	return f.Name
}

func (f *FakeObject) GetNamespace() string {
	return f.Namespace
}

type FakeCondition struct {
	Type   string
	Status string
	Reason string
}

func createBenchmarkScenario(tb testing.TB, registry *prometheus.Registry) *ConditionMetricRecorder {
	tb.Helper()

	ns := "bench_ns_" + generatedName("", tb.(*testing.B).N)
	gauge := NewOperatorConditionsGauge(ns)
	_ = registry.Register(gauge)
	tb.Cleanup(func() {
		registry.Unregister(gauge)
	})

	rec := &ConditionMetricRecorder{
		Controller:              controller,
		OperatorConditionsGauge: gauge,
	}

	obj := &FakeObject{}
	transitionTime := time.Now().UTC()

	condition := &FakeCondition{
		Status: "True", // doesn't matter, cardinality decided by Reason
	}

	for i := 0; i < controllerCount; i++ {
		kind := generatedName("Controller", i)

		for j := 0; j < resourcesPerController; j++ {
			obj.Name = generatedName("Resource", j)
			obj.Namespace = generatedName("namespace", j)

			for k := 0; k < conditionsPerController; k++ {
				condition.Type = generatedName("condition", k)

				for v := 0; v < variantsPerCondition; v++ {
					condition.Reason = generatedName("variant", v)
					rec.RecordConditionFor(kind, obj, condition.Type, condition.Reason, condition.Reason, transitionTime)
				}
			}
		}
	}

	return rec
}

// Benchmark the average time per call on a pre-populated scenario:
//   - RecordConditionFor
//   - RemoveConditionsFor
//
// Reports: ns/op for each sub-benchmark.
func Benchmark_ConditionMetricsRecorder_TimePerCall(b *testing.B) {
	reg := prometheus.NewRegistry()
	rec := createBenchmarkScenario(b, reg)

	// Use a stable object that exists in the populated dataset.
	kind := "Benchmark"
	obj := &FakeObject{
		Name:      "Resource0",
		Namespace: "namespace0",
	}
	transitionTime := time.Now().UTC()

	// Two variants in the same (controller,kind,name,namespace,condition) group.
	condTrue := &FakeCondition{
		Type:   "condition0",
		Status: "True",
		Reason: "variant0",
	}
	condFalse := &FakeCondition{
		Type:   "condition0",
		Status: "False",
		Reason: "variant0",
	}

	b.Run("RecordConditionFor", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.ReportMetric(float64(maxCardinality), "series/op")
		for i := 0; i < b.N; i++ {
			// Flip between two variants
			if (i & 1) == 0 {
				rec.RecordConditionFor(kind, obj, condTrue.Type, condTrue.Status, condTrue.Reason, transitionTime)
			} else {
				rec.RecordConditionFor(kind, obj, condFalse.Type, condFalse.Status, condFalse.Reason, transitionTime)
			}
		}
	})

	b.Run("RemoveConditionsFor", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.ReportMetric(float64(maxCardinality), "series/op")
		for i := 0; i < b.N; i++ {
			// Ensure there is something to remove, but do not count the set time.
			b.StopTimer()
			rec.RecordConditionFor(kind, obj, condTrue.Type, condTrue.Status, condTrue.Reason, transitionTime)
			b.StartTimer()

			rec.RemoveConditionsFor(kind, obj)
		}
	})
}

// Benchmark the size of the Prometheus gather output on a pre-populated scenario.
//
// Reports: Metric size in KB retrieved from the registry.
func Benchmark_ConditionMetricsRecorder_PrometheusMemorySize(b *testing.B) {
	reg := prometheus.NewRegistry()
	_ = createBenchmarkScenario(b, reg)

	b.ReportAllocs()
	b.ResetTimer()
	b.ReportMetric(float64(maxCardinality), "series/op")

	mfs, err := reg.Gather()
	if err != nil {
		b.Fatalf("gather: %v", err)
	}
	var buf bytes.Buffer
	for _, mf := range mfs {
		_, _ = expfmt.MetricFamilyToText(&buf, mf)
	}
	sizeKB := float64(buf.Len()) / 1024.0

	b.ReportMetric(sizeKB, "KB")
}
