/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SpinAppSpec defines the desired state of SpinApp
//
// +kubebuilder:validation:Optional
type SpinAppSpec struct {
	// Executor controls how this app is executed in the cluster.
	//
	// Defaults to whatever executor is available on the cluster. If multiple
	// executors are available then the first executor in alphabetical order
	// will be chosen. If no executors are available then no default will be set.
	Executor string `json:"executor"`

	// Image is the source for this app.
	//
	// +kubebuilder:validation:Required
	Image string `json:"image"`

	// ImagePullSecrets is a list of references to secrets in the same namespace to use for pulling the image.
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Checks defines health checks that should be used by Kubernetes to monitor the application.
	Checks HealthChecks `json:"checks,omitempty"`

	// Number of replicas to run.
	Replicas int32 `json:"replicas,omitempty"`

	// EnableAutoscaling indicates whether the app is allowed to autoscale. If
	// true then the operator leaves the replica count of the underlying
	// deployment to be managed by an external autoscaler (HPA/KEDA). Replicas
	// cannot be defined if this is enabled. By default EnableAutoscaling is false.
	//
	// +kubebuilder:default:=false
	EnableAutoscaling bool `json:"enableAutoscaling,omitempty"`

	// RuntimeConfig defines configuration to be applied at runtime for this app.
	RuntimeConfig RuntimeConfig `json:"runtimeConfig,omitempty"`

	// Volumes defines the volumes to be mounted in the underlying pods.
	Volumes []corev1.Volume `json:"volumes,omitempty"`

	// VolumeMounts defines how volumes are mounted in the underlying containers.
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Variables provide Kubernetes Bindings to Spin App Variables.
	Variables []SpinVar `json:"variables,omitempty"`

	// ServiceAnnotations defines annotations to be applied to the underlying service.
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// DeploymentAnnotations defines annotations to be applied to the underlying deployment.
	DeploymentAnnotations map[string]string `json:"deploymentAnnotations,omitempty"`

	// PodAnnotations defines annotations to be applied to the underlying pods.
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`

	// PodLabels defines labels to be applied to the underlying pods.
	PodLabels map[string]string `json:"podLabels,omitempty"`

	// Resources defines the resource requirements for this app.
	Resources Resources `json:"resources,omitempty"`

	// Components of the app to execute.
	//
	// If this is not provided all components are executed.
	// +kubebuilder:validation:MinItems:=1
	Components []string `json:"components,omitempty"`

	// WorkloadIdentity defines the workload identity configuration for cloud provider authentication.
	// +optional
	WorkloadIdentity *WorkloadIdentity `json:"workloadIdentity,omitempty"`
}

// SpinAppStatus defines the observed state of SpinApp
type SpinAppStatus struct {
	// Represents the observations of a SpinApps's current state.
	// SpinApp.status.conditions.type are: "Available" and "Progressing"
	// SpinApp.status.conditions.status are one of True, False, Unknown.
	// SpinApp.status.conditions.reason the value should be a CamelCase string and producers of specific
	// condition types may define expected values and meanings for this field, and whether the values
	// are considered a guaranteed API.
	// SpinApp.status.conditions.Message is a human readable message indicating details about the transition.
	// For further information see: https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

	// ActiveScheduler is the name of the scheduler that is currently scheduling this SpinApp.
	ActiveScheduler string `json:"activeScheduler,omitempty"`

	// Represents the current number of active replicas on the application deployment.
	ReadyReplicas int32 `json:"readyReplicas"`
}

// SpinApp is the Schema for the spinapps API
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".status.readyReplicas",name=Ready,type=integer
// +kubebuilder:printcolumn:JSONPath=".spec.replicas",name=Desired,type=integer
// +kubebuilder:printcolumn:JSONPath=".spec.executor",name=Executor,type=string
type SpinApp struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SpinAppSpec   `json:"spec,omitempty"`
	Status SpinAppStatus `json:"status,omitempty"`
}

// SpinAppList contains a list of SpinApp
//
// +kubebuilder:object:root=true
type SpinAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []SpinApp `json:"items"`
}

// RuntimeConfig defines configuration to be applied at runtime for this app.
type RuntimeConfig struct {
	// LoadFromSecret is the name of the secret to load runtime config from. The
	// secret should have a single key named "runtime-config.toml" that contains
	// the base64 encoded runtime config. If this is provided all other runtime
	// config is ignored.
	//
	// +optional
	LoadFromSecret string `json:"loadFromSecret,omitempty"`

	// SqliteDatabases provides spin bindings to different SQLite database providers.
	// e.g on-disk or turso.
	SqliteDatabases []SqliteDatabaseConfig `json:"sqliteDatabases,omitempty"`

	KeyValueStores []KeyValueStoreConfig `json:"keyValueStores,omitempty"`

	LLMCompute *LLMComputeConfig `json:"llmCompute,omitempty"`
}

type SqliteDatabaseConfig struct {
	Name    string                `json:"name"`
	Type    string                `json:"type"`
	Options []RuntimeConfigOption `json:"options,omitempty"`
}

type KeyValueStoreConfig struct {
	Name    string                `json:"name"`
	Type    string                `json:"type"`
	Options []RuntimeConfigOption `json:"options,omitempty"`
}

type LLMComputeConfig struct {
	Type    string                `json:"type"`
	Options []RuntimeConfigOption `json:"options,omitempty"`
}

type RuntimeConfigOption struct {
	// Name of the config option.
	Name string `json:"name"`

	// Value is the static value to bind to the variable.
	//
	// +optional
	Value string `json:"value,omitempty"`

	// ValueFrom is a reference to dynamically bind the variable to.
	//
	// +optional
	ValueFrom *RuntimeConfigVarSource `json:"valueFrom,omitempty"`
}

type RuntimeConfigVarSource struct {
	// Selects a key of a ConfigMap.
	// +optional
	ConfigMapKeyRef *corev1.ConfigMapKeySelector `json:"configMapKeyRef,omitempty"`

	// Selects a key of a secret in the apps namespace
	// +optional
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// SpinVar defines a binding between a spin variable and a static or dynamic value.
type SpinVar struct {
	// Name of the variable to bind.
	Name string `json:"name"`

	// Value is the static value to bind to the variable.
	//
	// +optional
	Value string `json:"value,omitempty"`

	// ValueFrom is a reference to dynamically bind the variable to.
	//
	// +optional
	ValueFrom *corev1.EnvVarSource `json:"valueFrom,omitempty"`
}

// Resources defines the resource requirements for this app.
type Resources struct {
	// Limits describes the maximum amount of compute resources allowed.
	Limits corev1.ResourceList `json:"limits,omitempty"`

	// Requests describes the minimum amount of compute resources required.
	// If Requests is omitted for a container, it defaults to Limits if that is explicitly specified,
	// otherwise to an implementation-defined value. Requests cannot exceed Limits.
	Requests corev1.ResourceList `json:"requests,omitempty"`
}

// HealthChecks defines configuration for readiness and liveness probes for the
// application.
type HealthChecks struct {
	// Readiness defines the readiness probe for the application.
	Readiness *HealthProbe `json:"readiness,omitempty"`

	// Liveness defines the liveness probe for the application.
	Liveness *HealthProbe `json:"liveness,omitempty"`
}

// HealthProbe defines an individual health check for an application.
type HealthProbe struct {
	// HTTPGet describes a health check that should be performed using a GET request.
	HTTPGet *HTTPHealthProbe `json:"httpGet,omitempty"`

	// Number of seconds after the app has started before liveness probes are initiated.
	// Default 10s.
	//
	// +kubebuilder:default:=10
	InitialDelaySeconds int32 `json:"initialDelaySeconds,omitempty"`

	// Number of seconds after which the probe times out.
	// Defaults to 1 second. Minimum value is 1.
	//
	// +kubebuilder:default:=1
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// How often (in seconds) to perform the probe.
	// Default to 10 seconds. Minimum value is 1.
	//
	// +optional
	// +kubebuilder:default:=10
	PeriodSeconds int32 `json:"periodSeconds,omitempty"`

	// Minimum consecutive successes for the probe to be considered successful after having failed.
	// Defaults to 1. Must be 1 for liveness and startup. Minimum value is 1.
	//
	// +optional
	// +kubebuilder:default:=1
	SuccessThreshold int32 `json:"successThreshold,omitempty"`

	// Minimum consecutive failures for the probe to be considered failed after having succeeded.
	// Defaults to 3. Minimum value is 1.
	//
	// +optional
	// +kubebuilder:default:=3
	FailureThreshold int32 `json:"failureThreshold,omitempty"`
}

// HTTPHealthProbe defines a HealthProbe that should use HTTP to call the application.
type HTTPHealthProbe struct {
	// Path is the path that should be used when calling the application for a
	// health check, e.g /healthz.
	Path string `json:"path"`

	// HTTPHeaders are headers that should be included in the health check request.
	//
	// +optional
	HTTPHeaders []HTTPHealthProbeHeader `json:"httpHeaders"`
}

// HTTPHealthProbeHeader is an abstraction around a http header key/value pair.
type HTTPHealthProbeHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// WorkloadIdentity defines the configuration for cloud provider workload identity.
type WorkloadIdentity struct {
	// ServiceAccountName is the name of the Kubernetes service account to use for workload identity.
	// +kubebuilder:validation:Required
	ServiceAccountName string `json:"serviceAccountName"`

	// ProviderMetadata contains cloud provider-specific configuration for workload identity.
	// +kubebuilder:validation:Required
	ProviderMetadata map[string]string `json:"providerMetadata"`
}

func init() {
	SchemeBuilder.Register(&SpinApp{}, &SpinAppList{})
}
