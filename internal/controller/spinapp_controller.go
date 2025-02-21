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

package controller

import (
	"context"
	"errors"
	"fmt"
	"hash/adler32"
	"maps"
	"strings"

	"github.com/pelletier/go-toml/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	spinv1alpha1 "github.com/spinkube/spin-operator/api/v1alpha1"
	"github.com/spinkube/spin-operator/internal/cacerts"
	"github.com/spinkube/spin-operator/internal/generics"
	"github.com/spinkube/spin-operator/internal/logging"
	"github.com/spinkube/spin-operator/internal/runtimeconfig"
	"github.com/spinkube/spin-operator/pkg/spinapp"
)

const (
	// HTTPAppPortName is the name of the port serving an app
	HTTPAppPortName = "http-app"

	// SpinOperatorFinalizer is the finalizer used by the spin operator
	SpinOperatorFinalizer = "core.spinkube.dev/finalizer"

	// FieldManager is used to declare that the spin operator owns specific fields on child resources
	FieldManager = "spin-operator"
)

// SpinAppReconciler reconciles a SpinApp object
type SpinAppReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=core.spinkube.dev,resources=spinapps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.spinkube.dev,resources=spinapps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// SetupWithManager sets up the controller with the Manager.
func (r *SpinAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&spinv1alpha1.SpinApp{}).
		// Owns allows watching dependency resources for any changes
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *SpinAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logging.FromContext(ctx)
	log.Debug("Reconciling SpinApp")

	// Check if the SpinApp exists
	var spinApp spinv1alpha1.SpinApp
	if err := r.Client.Get(ctx, req.NamespacedName, &spinApp); err != nil {
		// TODO: This error logging is noisy
		log.Error(err, "Unable to fetch SpinApp")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var executor spinv1alpha1.SpinAppExecutor
	if err := r.Client.Get(ctx, types.NamespacedName{
		// Executors must currently be defined in the same namespace as the app.
		// When we decide if the operator will be global or namespaced we may want
		// executors to be global as they're a platform concern.
		Namespace: req.NamespacedName.Namespace,
		Name:      spinApp.Spec.Executor,
	}, &executor); err != nil {
		log.Error(err, "unable to fetch executor")
		r.Recorder.Event(&spinApp, "Warning", "MissingExecutor",
			fmt.Sprintf("Could not find SpinAppExecutor %s/%s", req.NamespacedName.Namespace, spinApp.Spec.Executor))
		return ctrl.Result{}, err
	}

	// Update the status of the SpinApp
	err := r.updateStatus(ctx, &spinApp, &executor)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Spin app has been requested for deletion, child resources will
	// automatically be deleted.
	if !spinApp.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Reconcile the child resources

	if executor.Spec.CreateDeployment {
		err := r.reconcileDeployment(ctx, &spinApp, executor.Spec.DeploymentConfig)
		if err != nil {
			log.Error(err, "Failed to Reconcile Deployment")
			return ctrl.Result{}, err
		}
	} else {
		// If we shouldn't be managing a deployment for an application ensure any
		// previously created deployments have been cleaned up.
		err := r.deleteDeployment(ctx, &spinApp)
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
	}

	err = r.reconcileService(ctx, &spinApp)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// updateStatus updates the status of a SpinApp.
func (r *SpinAppReconciler) updateStatus(ctx context.Context, app *spinv1alpha1.SpinApp, executor *spinv1alpha1.SpinAppExecutor) error {
	log := logging.FromContext(ctx)

	// Our only status management is currently based on the resulting deployment
	// because of this, lets skip status interactions when the deployment is disabled.
	if !executor.Spec.CreateDeployment {
		return nil
	}

	// Set the active scheduler
	app.Status.ActiveScheduler = app.Spec.Executor

	deployment, err := r.findDeploymentForApp(ctx, app)
	if client.IgnoreNotFound(err) != nil {
		log.Error(err, "Unable to find deployment for app")
		return err
	}

	if apierrors.IsNotFound(err) {
		// Deployment doesn't exist yet so set conditions as unknown
		meta.SetStatusCondition(
			&app.Status.Conditions,
			metav1.Condition{
				Type:    "Available",
				Status:  metav1.ConditionUnknown,
				Reason:  "DeploymentNotFound",
				Message: "Deployment not found",
			})
		meta.SetStatusCondition(
			&app.Status.Conditions,
			metav1.Condition{
				Type:    "Progressing",
				Status:  metav1.ConditionUnknown,
				Reason:  "DeploymentNotFound",
				Message: "Deployment not found",
			})
		app.Status.ReadyReplicas = 0
	} else {
		deploymentConditions := deployment.Status.Conditions
		for _, dc := range deploymentConditions {
			if dc.Type == appsv1.DeploymentAvailable {
				meta.SetStatusCondition(
					&app.Status.Conditions,
					metav1.Condition{
						Type:    "Available",
						Status:  metav1.ConditionStatus(dc.Status),
						Reason:  dc.Reason,
						Message: dc.Message,
					})
			}
			if dc.Type == appsv1.DeploymentProgressing {
				meta.SetStatusCondition(
					&app.Status.Conditions,
					metav1.Condition{
						Type:    "Progressing",
						Status:  metav1.ConditionStatus(dc.Status),
						Reason:  dc.Reason,
						Message: dc.Message,
					})
			}
		}
		app.Status.ReadyReplicas = deployment.Status.ReadyReplicas
	}

	if err := r.Client.Status().Update(ctx, app); err != nil {
		log.Error(err, "Unable to update status")
	}

	// Re-fetch app to avoid "object has been modified" errors
	if err := r.Client.Get(ctx, types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, app); err != nil {
		log.Error(err, "Unable to re-fetch app")
		return err
	}

	return nil
}

const defaultCASecretName = "spin-ca"

// ensureCASecret creates the ca certificate bundle in the
// namespace of the app. Only one is required per namespace. The secret can be
// overridden by the cluster operator.
func (r *SpinAppReconciler) ensureCASecret(ctx context.Context, caSecretName, namespace string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      caSecretName,
			Namespace: namespace,
			Annotations: map[string]string{
				"app.kubernetes.io/managed-by": "spin-operator.spinkube.dev",
			},
		},
		StringData: map[string]string{"ca-certificates.crt": cacerts.CACertificates()},
	}

	err := r.Client.Get(ctx, types.NamespacedName{Name: caSecretName, Namespace: namespace}, secret)
	if !apierrors.IsNotFound(err) {
		return nil
	}
	return r.Client.Create(ctx, secret)
}

// reconcileDeployment creates a deployment if one does not exist and reconciles it if it does.
func (r *SpinAppReconciler) reconcileDeployment(ctx context.Context, app *spinv1alpha1.SpinApp, config *spinv1alpha1.ExecutorDeploymentConfig) error {
	log := logging.FromContext(ctx).WithValues("deployment", app.Name)

	rcBuilder := runtimeconfig.NewBuilder(r.Client)

	generatedRuntimeConfig, err := rcBuilder.Build(ctx, app)
	if err != nil {
		return fmt.Errorf("failed to construct RuntimeConfig: %w", err)
	}

	var generatedRuntimeConfigSecretName string

	if generatedRuntimeConfig != nil {
		tomlValue, err := toml.Marshal(generatedRuntimeConfig)
		if err != nil {
			return fmt.Errorf("failed to marshal RuntimeConfig: %w", err)
		}

		// A checksum of the rendered runtimeConfig acts as a unique-enough value to
		// ensure we don't reschedule apps unless the runtime config has changed.
		// Adler32 is probably fine here - if we run into collision issues then we
		// can switch to hashing-and-truncating.
		generatedRuntimeConfigSecretName = fmt.Sprintf("%s-%x", app.ObjectMeta.Name, adler32.Checksum(tomlValue))
		secret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Secret",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: app.ObjectMeta.Namespace,
				Name:      generatedRuntimeConfigSecretName,
				Labels: map[string]string{
					spinapp.NameLabelKey: app.ObjectMeta.Name,
				},
			},
			Data: map[string][]byte{
				"runtime-config.toml": tomlValue,
			},
		}
		err = controllerutil.SetOwnerReference(app, secret, r.Scheme)
		if err != nil {
			return fmt.Errorf("failed to set runtimeconfig owner reference: %w", err)
		}

		err = r.Client.Create(ctx, secret)
		if err != nil {
			if client.IgnoreAlreadyExists(err) != nil {
				return fmt.Errorf("failed to create RuntimeConfig secret: %w", err)
			}
			log.Debug("RuntimeConfig Secret already exists", "runtime_config_secret_name", secret.ObjectMeta.Name)
		}
	}

	caSecretName := config.CACertSecret
	if config.InstallDefaultCACerts {
		if caSecretName == "" {
			caSecretName = defaultCASecretName
		}
		if err := r.ensureCASecret(ctx, caSecretName, app.Namespace); err != nil {
			return fmt.Errorf("unable to create default ca-certificate secret: %w", err)
		}
	}

	desiredDeployment, err := constructDeployment(ctx, app, config, generatedRuntimeConfigSecretName, caSecretName, r.Scheme)
	if err != nil {
		return fmt.Errorf("failed to construct Deployment: %w", err)
	}

	log.Debug("Reconciling Deployment")

	// We want to use server-side apply https://kubernetes.io/docs/reference/using-api/server-side-apply
	patchMethod := client.Apply
	patchOptions := &client.PatchOptions{
		Force:        generics.Ptr(true), // Force b/c any fields we are setting need to be owned by the spin-operator
		FieldManager: FieldManager,
	}

	// Note that we reconcile even if the deployment is in a good state. We rely on controller-runtime to rate limit us.
	if err := r.Client.Patch(ctx, desiredDeployment, patchMethod, patchOptions); err != nil {
		log.Error(err, "Unable to reconcile Deployment")
		return err
	}

	return nil
}

// reconcileService creates a service if one does not exist and updates it if it does.
func (r *SpinAppReconciler) reconcileService(ctx context.Context, app *spinv1alpha1.SpinApp) error {
	log := logging.FromContext(ctx).WithValues("service", app.Name)

	desiredService := constructService(app)
	if err := ctrl.SetControllerReference(app, desiredService, r.Scheme); err != nil {
		log.Error(err, "Unable to construct Service")
		return err
	}

	log.Debug("Reconciling Service")

	// We want to use server-side apply https://kubernetes.io/docs/reference/using-api/server-side-apply
	patchMethod := client.Apply
	patchOptions := &client.PatchOptions{
		Force:        generics.Ptr(true), // Force b/c any fields we are setting need to be owned by the spin-operator
		FieldManager: FieldManager,
	}
	// Note that we reconcile even if the service is in a good state. We rely on controller-runtime to rate limit us.
	if err := r.Client.Patch(ctx, desiredService, patchMethod, patchOptions); err != nil {
		log.Error(err, "Unable to reconcile Service")
		return err
	}

	return nil
}

// deleteDeployment deletes the deployment for a SpinApp.
func (r *SpinAppReconciler) deleteDeployment(ctx context.Context, app *spinv1alpha1.SpinApp) error {
	deployment, err := r.findDeploymentForApp(ctx, app)
	if err != nil {
		return err
	}

	err = r.Client.Delete(ctx, deployment)
	if err != nil {
		return err
	}

	return nil
}

// constructDeployment builds an appsv1.Deployment based on the configuration of a SpinApp.
func constructDeployment(ctx context.Context, app *spinv1alpha1.SpinApp, config *spinv1alpha1.ExecutorDeploymentConfig,
	generatedRuntimeConfigSecretName, caSecretName string, scheme *runtime.Scheme) (*appsv1.Deployment, error) {
	// TODO: Once we land admission webhooks write some validation to make
	// replicas and enableAutoscaling mutually exclusive.
	var replicas *int32
	if app.Spec.EnableAutoscaling {
		replicas = nil
	} else {
		replicas = generics.Ptr(app.Spec.Replicas)
	}

	volumes, volumeMounts, err := ConstructVolumeMountsForApp(ctx, app, generatedRuntimeConfigSecretName, caSecretName)
	if err != nil {
		return nil, err
	}

	annotations := app.Spec.DeploymentAnnotations
	if annotations == nil {
		annotations = map[string]string{}
	}
	templateAnnotations := app.Spec.PodAnnotations
	if templateAnnotations == nil {
		templateAnnotations = map[string]string{}
	}

	statusKey, statusValue := spinapp.ConstructStatusReadyLabel(app.Name)
	readyLabels := map[string]string{
		spinapp.NameLabelKey: app.Name,
		statusKey:            statusValue,
	}

	templateLabels := app.Spec.PodLabels
	if templateLabels == nil {
		templateLabels = map[string]string{}
	}
	maps.Copy(templateLabels, readyLabels)

	// Add Azure workload identity label if configured
	if app.Spec.WorkloadIdentity != nil {
		if val, ok := app.Spec.WorkloadIdentity.ProviderMetadata["azure"]; ok {
			if val == "true" {
				templateLabels["azure.workload.identity/use"] = "true"
			}
		}
	}

	// TODO: Once we land admission webhooks write some validation for this e.g.
	// don't allow setting memory limit with cyclotron runtime.
	resources := corev1.ResourceRequirements{
		Limits:   app.Spec.Resources.Limits,
		Requests: app.Spec.Resources.Requests,
	}

	env := ConstructEnvForApp(ctx, app, spinapp.DefaultHTTPPort, config.Otel)
	if app.Spec.Components != nil {
		env = append(env, corev1.EnvVar{
			Name:  "SPIN_COMPONENTS_TO_RETAIN",
			Value: strings.Join(app.Spec.Components[:], ","),
		})
	}

	readinessProbe, livenessProbe, err := ConstructPodHealthChecks(app)
	if err != nil {
		return nil, err
	}

	labels := constructAppLabels(app)

	var container corev1.Container
	if config.RuntimeClassName != nil {
		container = corev1.Container{
			Name:    app.Name,
			Image:   app.Spec.Image,
			Command: []string{"/"},
			Ports: []corev1.ContainerPort{{
				Name:          spinapp.HTTPPortName,
				ContainerPort: spinapp.DefaultHTTPPort,
			}},
			Env:            env,
			VolumeMounts:   volumeMounts,
			Resources:      resources,
			LivenessProbe:  livenessProbe,
			ReadinessProbe: readinessProbe,
		}
	} else if config.SpinImage != nil {
		args := []string{"up", "--listen", fmt.Sprintf("0.0.0.0:%d", spinapp.DefaultHTTPPort), "-f", app.Spec.Image, "--runtime-config-file", "/runtime-config.toml"}
		if app.Spec.Components != nil {
			for _, component := range app.Spec.Components {
				args = append(args, "--component-id", component)
			}
		}
		container = corev1.Container{
			Name:  app.Name,
			Image: *config.SpinImage,
			// TODO: add support for --component-id flags to set components to be retained once spintainer supports Spin v3.0
			Args: args,
			Ports: []corev1.ContainerPort{{
				Name:          spinapp.HTTPPortName,
				ContainerPort: spinapp.DefaultHTTPPort,
			}},
			Env:            env,
			VolumeMounts:   volumeMounts,
			Resources:      resources,
			LivenessProbe:  livenessProbe,
			ReadinessProbe: readinessProbe,
		}
	} else {
		return nil, errors.New("must specify either runtimeClassName or spinImage")
	}

	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        app.Name,
			Namespace:   app.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: readyLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      templateLabels,
					Annotations: templateAnnotations,
				},
				Spec: corev1.PodSpec{
					RuntimeClassName:   config.RuntimeClassName,
					ServiceAccountName: getServiceAccountName(app),
					Containers:         []corev1.Container{container},
					ImagePullSecrets:   app.Spec.ImagePullSecrets,
					Volumes:            volumes,
				},
			},
		},
	}

	// Set the controller reference, specifying that these resources are controlled by the SpinApp
	// being reconciled
	// TODO: Move this out of the "constructor" or otherwise abstract the setter
	//       to not depend on controller-runtime api for testing "pure" data code.
	if scheme != nil {
		if err := ctrl.SetControllerReference(app, dep, scheme); err != nil {
			return nil, err
		}
	}

	return dep, nil
}

// getServiceAccountName returns the service account name to use for the deployment.
// If workload identity is configured, it returns the configured service account name.
// Otherwise, it returns "default" which is the Kubernetes default.
func getServiceAccountName(app *spinv1alpha1.SpinApp) string {
	if app.Spec.WorkloadIdentity != nil {
		return app.Spec.WorkloadIdentity.ServiceAccountName
	}
	return "default"
}

// findDeploymentForApp finds the deployment for a SpinApp.
func (r *SpinAppReconciler) findDeploymentForApp(ctx context.Context, app *spinv1alpha1.SpinApp) (*appsv1.Deployment, error) {
	var deployment appsv1.Deployment
	err := r.Client.Get(ctx, types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, &deployment)
	if err != nil {
		return nil, err
	}
	return &deployment, nil
}
