# CRD Condition Metrics

A Go package for Kubernetes operators. It exports the `status.conditions` of your custom resources as Prometheus
metrics, and it keeps the metrics in line with the
[status conditions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties)
that your controller writes.

This package is built on the [Prometheus GaugeVecSet implementation for Go](https://github.com/sourcehawk/go-prometheus-gaugevecset).

## Table of Contents

1. [Features](#features)
2. [Installation](#installation)
3. [Motivation](#motivation)
4. [The metric](#the-metric)
5. [Setup: Operator initialization](#operator-initialization)
6. [Setup: Controller usage](#controller-usage)
7. [Removing metrics](#removing-metrics)
8. [Check that the metrics are exported](#check-that-the-metrics-are-exported)
9. [Running in production](#running-in-production)
10. [PromQL usage examples](#promql-usage-examples)

---

## Features

- **Metrics that follow your status conditions.** You record a condition after you store it in the status, and
  the metric then shows the stored state.
- **One series per condition.** The package keeps one series per (custom resource, condition type). When the
  status or the reason changes, the old series is removed.
- **The time of the last transition as the value.** You can query how long a resource has been in its current
  state.
- **Optional labels of your own**, such as a tenant or a team label.
- **[Grafana dashboards](https://github.com/sourcehawk/go-crd-condition-metrics/tree/master/dashboards)** and
  **[Prometheus alert rules](https://github.com/sourcehawk/go-crd-condition-metrics/tree/master/alerts)** for your
  metric name. In a clone of this repository, generate them with `make dashboards METRIC_NAMESPACE=my_operator` and
  `make alerts METRIC_NAMESPACE=my_operator`. The pages behind the links explain each one.

The repository has benchmarks for the time per call and for the memory size. In a clone, run them with
`make benchmark`.

---

## Installation

```bash
go get github.com/sourcehawk/go-crd-condition-metrics
```

Import the package:

```go
import (
	ccm "github.com/sourcehawk/go-crd-condition-metrics/pkg/crd-condition-metrics"
)
```

---

## Motivation

Metrics for custom resources are an important part of observability for a Kubernetes operator. But there is no
standard way to export metrics that show the actual `status` of a custom resource.

In Kubernetes, the `status.conditions` field is the usual way to show the state of a resource. It holds lifecycle
signals such as `Ready`, `Reconciled`, `Degraded`, or `FailedToProvision`, with a `status`, a `reason`, and a
`lastTransitionTime` for each one.

This package exports those conditions as metrics in one consistent form, so that you can:
- Get metrics directly from the `status.conditions` of your resources
- Keep the metric values and labels in line with the stored resource state
- Keep the metric cardinality low
- See when each condition last changed

### Pattern inspiration: kube-state-metrics

This metric is based on `kube_pod_status_phase` from [kube-state-metrics](https://github.com/kubernetes/kube-state-metrics).
That metric exports one series per `phase` for each `(namespace, pod)` pair. It sets the active phase to `1` and the
other phases to `0`:

```
kube_pod_status_phase{namespace="default", pod="nginx", phase="Running"} 1
kube_pod_status_phase{namespace="default", pod="nginx", phase="Pending"} 0
kube_pod_status_phase{namespace="default", pod="nginx", phase="Failed"}  0
```

This package uses a similar idea for `status.conditions`, with two differences:

- It exports **only one series per (custom resource, condition type)**. When a new status or reason is set, the
  series with the old status and reason is removed.
- The value is not `1` or `0`. It is the **Unix timestamp of `lastTransitionTime`**, in seconds, so you can query
  when the condition last changed.

A series from this package:

```
my_operator_controller_condition{
    condition="Ready",
    controller="my-controller",
    id="default/my-cr",
    kind="MyCR",
    name="my-cr",
    namespace="default",
    reason="ProvisioningFailed",
    status="False"
} 1759174321
```

With this metric you can make dashboards and alerts that:
- Show all custom resources that are not `Ready` now
- Alert when a custom resource stays in a condition for too long
- Show how long a custom resource has had its current status

---

## The metric

### Name

The metric name is `<metric namespace>_controller_condition`. The metric namespace is the first argument to
`NewOperatorConditionsGauge`. It is a prefix for the metric name, and it is not related to Kubernetes namespaces.
For example, `ccm.NewOperatorConditionsGauge("my_operator")` gives the name `my_operator_controller_condition`.

### Labels

| Label | Value |
|---|---|
| `controller` | The `Controller` field of the `ConditionMetricRecorder` |
| `kind` | The `kind` argument that you give to `RecordConditionFor` |
| `name` | The name of the custom resource |
| `namespace` | The namespace of the custom resource. It is empty for a cluster-scoped resource. |
| `condition` | The condition type, such as `Ready` |
| `status` | The condition status: `True`, `False`, or `Unknown` |
| `reason` | The condition reason |
| `id` | `<namespace>/<name>`. For a cluster-scoped resource it is `/<name>`. |
| Your extra labels | The values that you give to `RecordConditionFor` (see [Extra labels](#extra-labels)) |

The package keeps one series for each (`controller`, `kind`, `name`, `namespace`, `condition`). So a resource has
one series for each condition type that a controller records for it. When a new `status` or `reason` is recorded for
the same condition type, the package removes the old series.

### Value

The value is the `lastTransitionTime` of the condition, as a Unix timestamp in seconds. If the `lastTransitionTime`
is zero, the package uses the time of the call. Then the value changes on each call, and the value no longer shows
how long the resource has had its status. `meta.SetStatusCondition` sets the `lastTransitionTime` for you.

### Extra labels

To add labels of your own, give their names to `NewOperatorConditionsGauge` after the metric namespace:

```go
OperatorConditionsGauge = ccm.NewOperatorConditionsGauge("my_operator", "tenant")
```

Then give one value for each extra label, in the same order, as the last arguments of `RecordConditionFor`:

```go
r.Recorder.RecordConditionFor(kind, cr, c.Type, string(c.Status), c.Reason, c.LastTransitionTime.Time, cr.Spec.Tenant)
```

`RecordConditionFor` panics if the number of extra values is not the same as the number of extra labels. All the
controllers that share the gauge must give the extra values.

The extra labels are not part of the key in [Labels](#labels). When an extra value changes, the new series replaces
the old series of that condition. The remove methods do not take the extra values.

---

## Operator initialization

Make and register the gauge one time, for all the controllers of your operator.

You can embed the `ConditionMetricRecorder` in the metrics recorder of your controller.

```go
package mymetrics

import (
	ccm "github.com/sourcehawk/go-crd-condition-metrics/pkg/crd-condition-metrics"
	controllermetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// OperatorConditionsGauge is shared by the recorders of all controllers.
var OperatorConditionsGauge *ccm.OperatorConditionsGauge

func init() {
	OperatorConditionsGauge = ccm.NewOperatorConditionsGauge("my_operator")
	controllermetrics.Registry.MustRegister(OperatorConditionsGauge)
}

// MyControllerRecorder is the metrics recorder of one controller.
type MyControllerRecorder struct {
	ccm.ConditionMetricRecorder
}
```

When you make your reconciler, give it a recorder with the shared gauge and a name for the controller. Use a
different name for each controller. The name becomes the value of the `controller` label.

_cmd/main.go_
```go
package main

import (
	ccm "github.com/sourcehawk/go-crd-condition-metrics/pkg/crd-condition-metrics"

	"path/to/internal/controller"
	mymetrics "path/to/pkg/mymetrics"
)

func main() {
	// ...
	recorder := mymetrics.MyControllerRecorder{
		ConditionMetricRecorder: ccm.ConditionMetricRecorder{
			Controller:              "my-controller",
			OperatorConditionsGauge: mymetrics.OperatorConditionsGauge,
		},
	}

	reconciler := &controller.MyReconciler{
		Client:   mgr.GetClient(),
		Recorder: recorder,
	}
	// ...
}
```

---

## Controller usage

Record only conditions that are stored in the cluster. If you record a condition before the status update, and the
update fails, the metric shows a condition that is not stored. The example records at two points:

1. Right after the `Get`. The conditions are stored at that point. So a reconcile that fails later still records
   them, and after a restart of the operator the metrics come back for each resource, also a resource whose
   reconcile fails.
2. After a status update succeeds, so that the metrics show the new conditions.

Your custom resource needs a `Status.Conditions` field of type `[]metav1.Condition`.

```go
package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "path/to/api/v1"
	mymetrics "path/to/pkg/mymetrics"
)

const kind = "MyCR"

type MyReconciler struct {
	client.Client
	Recorder mymetrics.MyControllerRecorder
}

func (r *MyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cr := &v1.MyCR{}
	if err := r.Get(ctx, req.NamespacedName, cr); err != nil {
		if apierrors.IsNotFound(err) {
			// The CR is gone: remove its metrics so that they do not go stale
			r.Recorder.RemoveConditionsFor(kind, &metav1.ObjectMeta{
				Name:      req.Name,
				Namespace: req.Namespace,
			})
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	r.recordConditions(cr)

	// ... reconcile the resource and decide its conditions

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		ObservedGeneration: cr.Generation,
	}
	if meta.SetStatusCondition(&cr.Status.Conditions, condition) {
		if err := r.Status().Update(ctx, cr); err != nil {
			return ctrl.Result{}, err
		}
		r.recordConditions(cr)
	}

	return ctrl.Result{}, nil
}

// recordConditions records a metric for each condition in the status of cr.
// Call it only after the status is stored.
func (r *MyReconciler) recordConditions(cr *v1.MyCR) {
	for _, c := range cr.Status.Conditions {
		r.Recorder.RecordConditionFor(
			kind, cr, c.Type, string(c.Status), c.Reason, c.LastTransitionTime.Time,
		)
	}
}
```

`RecordConditionFor` and the remove methods accept any object that has the `GetName()` and `GetNamespace()`
methods. Your custom resource type and `*metav1.ObjectMeta` both have them.

---

## Removing metrics

### When a resource is deleted

Call `RemoveConditionsFor` when the `Get` returns a `NotFound` error, as in the example above. At that point the
resource is gone, so no later reconcile can record its conditions again. The resource object is not available, so
give a `*metav1.ObjectMeta` with the name and namespace of the request.

Do not remove the metrics when the resource has a deletion timestamp. Without a finalizer, the controller usually
does not see that state. With a finalizer, a later reconcile can record a condition again after the removal. In both
cases, the last reconcile gets `NotFound`. If it returns without a removal, the metrics stay.

`RemoveConditionsFor` returns the number of series that it removed. It returns 0 when nothing was recorded for the
resource, so you can call it on each `NotFound`.

### When a condition type is removed from the status

When you remove a condition type from the status, also remove its series with `RemoveConditionFor`. The series of the
other condition types stay. `RemoveConditionFor` returns the number of series that it removed, and 0 when nothing was
recorded for that condition type. Remove it after the status update succeeds:

```go
if meta.RemoveStatusCondition(&cr.Status.Conditions, "Degraded") {
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, err
	}
	r.Recorder.RemoveConditionFor(kind, cr, "Degraded")
}
```

If you do not remove it, the series stays with its last value until `RemoveConditionsFor` removes it or the operator
restarts.

---

## Check that the metrics are exported

Forward a local port to the metrics port of the operator pod, and get the metrics. If you use leader election, use
the leader pod: the other replicas export no series.

```bash
kubectl -n <operator-namespace> port-forward pod/<operator-pod> 8080:<metrics-port>
curl -s http://localhost:8080/metrics | grep _controller_condition
```

If your metrics endpoint uses HTTPS and authentication, as the kubebuilder default does, use `https://` and send a
bearer token that has access to the endpoint.

You see a `# HELP` line, a `# TYPE ... gauge` line, and one line for each condition of each resource that your
controller reconciled.

---

## Running in production

**Concurrent reconciles.** The recorder is safe for concurrent use, because the gauge locks its state. You can set
`MaxConcurrentReconciles` to more than 1.

**Leader election.** Only the leader runs reconciles, so only the leader records metrics. The other replicas export
no series. When the leadership moves, the old leader can export its last series until it stops. By default, a
controller-runtime manager stops when it loses its lease. During that period, a query with `count()` can count a
resource two times. The generated
[alert rules](https://github.com/sourcehawk/go-crd-condition-metrics/tree/master/alerts) aggregate with `max()` by
resource, so that two series for one resource give one alert.

**Restarts.** The metrics are kept in memory. After a restart, or on a new leader, they come back when each resource
is reconciled again. The initial sync of the controller reconciles all resources.

---

## PromQL usage examples

The examples use the metric name `my_operator_controller_condition`, from
`ccm.NewOperatorConditionsGauge("my_operator")`.

> [!IMPORTANT]
> Usually the scrape configuration adds a `namespace` label to each series: the namespace of the operator pod. With
> the default `honor_labels: false`, Prometheus then renames the `namespace` label of this package to
> `exported_namespace`. The examples use `namespace`. If your series have `exported_namespace`, use that label in
> its place. The generated dashboards use `exported_namespace`. The generated alerts use it by default. The
> [alerts page](https://github.com/sourcehawk/go-crd-condition-metrics/tree/master/alerts#the-namespace-label) shows
> how to change it.
>
> Prometheus does not rename an empty label. So for a cluster-scoped resource, `namespace` is the namespace of the
> operator pod, and there is no `exported_namespace`.

The example outputs leave out the labels that the scrape adds, such as `pod` and `instance`.

### Resources that are not ready

Get all resources of kind `App` whose `Ready` condition is `False`:

```promql
my_operator_controller_condition{
    kind="App",
    condition="Ready",
    status="False",
}
```

Output:

```
my_operator_controller_condition{condition="Ready", controller="my-controller", id="ns-1/my-app-1", kind="App", name="my-app-1", namespace="ns-1", reason="Foo", status="False"} 1759416292
my_operator_controller_condition{condition="Ready", controller="my-controller", id="ns-1/my-app-2", kind="App", name="my-app-2", namespace="ns-1", reason="Bar", status="False"} 1759329097
my_operator_controller_condition{condition="Ready", controller="my-controller", id="ns-2/my-app", kind="App", name="my-app", namespace="ns-2", reason="Foo", status="False"} 1759329145
my_operator_controller_condition{condition="Ready", controller="my-controller", id="ns-3/my-app", kind="App", name="my-app", namespace="ns-3", reason="Foo", status="False"} 1759406280
```

The same query when your series have `exported_namespace`, for the resources in `ns-1` only:

```promql
my_operator_controller_condition{
    kind="App",
    condition="Ready",
    status="False",
    exported_namespace="ns-1",
}
```

### Count the resources that are not ready

```promql
count(
  my_operator_controller_condition{
    kind="App",
    condition="Ready",
    status="False",
  }
)
```

Output:

```
{} 4
```

### How long each resource has had its current status

The value is the time of the last transition, so subtract it from the current time. The result is in seconds:

```promql
time() - my_operator_controller_condition{
    kind="App",
    condition="Ready",
}
```

Get the resources that have not been ready for more than one hour:

```promql
time() - my_operator_controller_condition{
    kind="App",
    condition="Ready",
    status!="True",
} > 3600
```
