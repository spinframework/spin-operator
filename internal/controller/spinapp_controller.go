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
	"fmt"

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

	spinv1 "github.com/spinkube/spin-operator/api/v1"
	"github.com/spinkube/spin-operator/internal/logging"
	"github.com/spinkube/spin-operator/pkg/spinapp"
)

const (
	// HTTPAppPortName is the name of the port serving an app
	HTTPAppPortName = "http-app"

	// SpinOperatorFinalizer is the finalizer used by the spin operator
	SpinOperatorFinalizer = "core.spinoperator.dev/finalizer"

	// FieldManger is used to declare that the spin operator owns specific fields on child resources
	FieldManager = "spin-operator"
)

// SpinAppReconciler reconciles a SpinApp object
type SpinAppReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=core.spinoperator.dev,resources=spinapps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.spinoperator.dev,resources=spinapps/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

// SetupWithManager sets up the controller with the Manager.
func (r *SpinAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&spinv1.SpinApp{}).
		// Owns allows watching dependency resources for any changes
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
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
	var spinApp spinv1.SpinApp
	if err := r.Client.Get(ctx, req.NamespacedName, &spinApp); err != nil {
		// TODO: This error logging is noisy
		log.Error(err, "Unable to fetch SpinApp")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var executor spinv1.SpinAppExecutor
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
func (r *SpinAppReconciler) updateStatus(ctx context.Context, app *spinv1.SpinApp, executor *spinv1.SpinAppExecutor) error {
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

// reconcileDeployment creates a deployment if one does not exist and reconciles it if it does.
func (r *SpinAppReconciler) reconcileDeployment(ctx context.Context, app *spinv1.SpinApp, config *spinv1.ExecutorDeploymentConfig) error {
	log := logging.FromContext(ctx).WithValues("deployment", app.Name)

	desiredDeployment, err := constructDeployment(ctx, app, config, r.Scheme)
	if err != nil {
		log.Error(err, "Unable to construct Deployment")
		return err
	}

	log.Debug("Reconciling Deployment")

	// We want to use server-side apply https://kubernetes.io/docs/reference/using-api/server-side-apply
	patchMethod := client.Apply
	patchOptions := &client.PatchOptions{
		Force:        ptr(true), // Force b/c any fields we are setting need to be owned by the spin-operator
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
func (r *SpinAppReconciler) reconcileService(ctx context.Context, app *spinv1.SpinApp) error {
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
		Force:        ptr(true), // Force b/c any fields we are setting need to be owned by the spin-operator
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
func (r *SpinAppReconciler) deleteDeployment(ctx context.Context, app *spinv1.SpinApp) error {
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
func constructDeployment(ctx context.Context, app *spinv1.SpinApp, config *spinv1.ExecutorDeploymentConfig, scheme *runtime.Scheme) (*appsv1.Deployment, error) {
	// TODO: Once we land admission webhooks write some validation to make
	// replicas and enableAutoscaling mutually exclusive.
	var replicas *int32
	if app.Spec.EnableAutoscaling {
		replicas = nil
	} else {
		replicas = ptr(app.Spec.Replicas)
	}

	volumes, volumeMounts, err := ConstructVolumeMountsForApp(ctx, app, "")
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

	// TODO: Once we land admission webhooks write some validation for this e.g.
	// don't allow setting memory limit with cyclotron runtime.
	resources := corev1.ResourceRequirements{
		Limits:   app.Spec.Resources.Limits,
		Requests: app.Spec.Resources.Requests,
	}

	env := ConstructEnvForApp(ctx, app)

	readinessProbe, livenessProbe, err := ConstructPodHealthChecks(app)
	if err != nil {
		return nil, err
	}

	labels := constructAppLabels(app)

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
					Labels:      readyLabels,
					Annotations: templateAnnotations,
				},
				Spec: corev1.PodSpec{
					RuntimeClassName: &config.RuntimeClassName,
					Containers: []corev1.Container{
						{
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
						},
					},
					ImagePullSecrets: app.Spec.ImagePullSecrets,
					Volumes:          volumes,
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

// findDeploymentForApp finds the deployment for a SpinApp.
func (r *SpinAppReconciler) findDeploymentForApp(ctx context.Context, app *spinv1.SpinApp) (*appsv1.Deployment, error) {
	var deployment appsv1.Deployment
	err := r.Client.Get(ctx, types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, &deployment)
	if err != nil {
		return nil, err
	}
	return &deployment, nil
}

func ptr[T any](v T) *T {
	return &v
}
