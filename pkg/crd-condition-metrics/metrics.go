package crd_condition_metrics

import (
	"fmt"
	"time"

	metrics "github.com/sourcehawk/go-prometheus-gaugevecset/pkg/gauge-vec-set"
)

const (
	operatorConditionMetricSubsystem = "controller"
	operatorConditionMetricName      = "condition"
	operatorConditionMetricHelp      = "Condition status for a custom resource; one time series per (custom resource, condition type) combination."
)

var (
	indexLabels = []string{"controller", "kind", "name", "namespace"}
	groupLabels = []string{"condition"}
	infoLabels  = []string{"status", "reason", "id"}
)

type OperatorConditionsGauge struct {
	*metrics.GaugeVecSet
}

// NewOperatorConditionsGauge creates a new OperatorConditionsGauge for an operator.
// Initialize once (e.g., in your package init or setup) and use in all implementations of ConditionMetricRecorder.
//
//	var OperatorConditionsGauge *OperatorConditionsGauge = nil
//
//	func init() {
//	  OperatorConditionsGauge = NewOperatorConditionsGauge("my-operator")
//	  controllermetrics.Registry.MustRegister(OperatorConditionsGauge)
//	}
func NewOperatorConditionsGauge(metricNamespace string, extraLabels ...string) *OperatorConditionsGauge {
	return &OperatorConditionsGauge{
		metrics.NewGaugeVecSet(
			metricNamespace,
			operatorConditionMetricSubsystem,
			operatorConditionMetricName,
			operatorConditionMetricHelp,
			indexLabels,
			groupLabels,
			append(infoLabels, extraLabels...)...,
		),
	}
}

type ObjectLike interface {
	GetName() string
	GetNamespace() string
}

// ConditionMetricRecorder records metrics for Kubernetes style `metav1.Condition`
// objects on custom resources, using a Prometheus gauge.
//
// Usage:
//
//	type MyControllerRecorder struct {
//		gvs.ConditionMetricRecorder
//	}
//
//	r := MyControllerRecorder{
//		ConditionMetricRecorder: gvs.ConditionMetricRecorder{
//			Controller: "my-controller",
//			OperatorConditionsGauge: my_metrics.OperatorConditionsGauge,
//		},
//	}
type ConditionMetricRecorder struct {
	// The name of the controller the condition metrics are for
	Controller string
	// The OperatorConditionsGauge initialized by NewOperatorConditionsGauge
	OperatorConditionsGauge *OperatorConditionsGauge
}

// RecordConditionFor sets a condition metric for a given controller and object.
//
// It enforces exclusivity within the same (custom resource, condition type), ensuring that only the latest
// (status, phase) is present for a given custom resource.
//
// If the last transition time is zero, the value of the metric is set to the unix timestamp for time.Now().UTC()
//
// Pass one extraLabelValues entry for each extra label given to NewOperatorConditionsGauge, in the same order.
// A different number of values panics.
//
// Example:
//
//	c := metav1.Condition{
//		Type: "Ready",
//		Status: metav1.ConditionTrue,
//		Reason: "AppReady",
//		LastTransitionTime: metav1.Now(),
//	}
//
//	r.RecordConditionFor(kind, obj, c.Type, string(c.Status), c.Reason, c.LastTransitionTime.Time)
func (r *ConditionMetricRecorder) RecordConditionFor(
	kind string, object ObjectLike,
	conditionType, conditionStatus, conditionReason string, lastTransitionTime time.Time,
	extraLabelValues ...string,
) {
	id := fmt.Sprintf("%s/%s", object.GetNamespace(), object.GetName())
	indexValues := []string{r.Controller, kind, object.GetName(), object.GetNamespace()}
	groupValues := []string{conditionType}
	extraValues := []string{conditionStatus, conditionReason, id}
	extraValues = append(extraValues, extraLabelValues...)

	if lastTransitionTime.IsZero() {
		lastTransitionTime = time.Now().UTC()
	}

	r.OperatorConditionsGauge.SetGroup(float64(lastTransitionTime.Unix()), indexValues, groupValues, extraValues...)
}

// RemoveConditionsFor deletes the series of every condition type that this controller recorded for the
// resource. Call it when the Get of the resource returns NotFound. If you call it while the resource still
// exists, a later reconcile records the conditions again.
//
// Returns the number of series deleted, which is 0 when nothing was recorded for the resource.
func (r *ConditionMetricRecorder) RemoveConditionsFor(kind string, object ObjectLike) (removed int) {
	return r.OperatorConditionsGauge.DeleteByIndex(r.Controller, kind, object.GetName(), object.GetNamespace())
}

// RemoveConditionFor deletes the series of one condition type that this controller recorded for the
// resource, and keeps the series of its other condition types. Call it when you remove the condition from
// the status, for example with meta.RemoveStatusCondition.
//
// Returns the number of series deleted, which is 0 when nothing was recorded for that condition type.
func (r *ConditionMetricRecorder) RemoveConditionFor(kind string, object ObjectLike, conditionType string) (removed int) {
	return r.OperatorConditionsGauge.DeleteByGroup(
		[]string{r.Controller, kind, object.GetName(), object.GetNamespace()}, conditionType,
	)
}
