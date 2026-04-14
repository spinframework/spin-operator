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

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	spinv1alpha1 "github.com/spinframework/spin-operator/api/v1alpha1"
	"github.com/spinframework/spin-operator/internal/logging"
)

var (
	spinAppExecutorKey = "spec.executor"
)

// SpinAppExecutorReconciler reconciles a SpinAppExecutor object
type SpinAppExecutorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=core.spinkube.dev,resources=spinappexecutors,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.spinkube.dev,resources=spinappexecutors/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.spinkube.dev,resources=spinappexecutors/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// SetupWithManager sets up the controller with the Manager.
func (r *SpinAppExecutorReconciler) SetupWithManager(mgr ctrl.Manager) error {

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &spinv1alpha1.SpinApp{}, spinAppExecutorKey, func(rawObj client.Object) []string {
		// grab the spinapp object, extract the executor...
		spinapp := rawObj.(*spinv1alpha1.SpinApp)
		return []string{spinapp.Spec.Executor}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&spinv1alpha1.SpinAppExecutor{}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *SpinAppExecutorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logging.FromContext(ctx)
	log.Debug("Reconciling SpinAppExecutor")

	// Check if the SpinAppExecutor exists
	var executor spinv1alpha1.SpinAppExecutor
	if err := r.Client.Get(ctx, req.NamespacedName, &executor); err != nil {
		log.Error(err, "Unable to fetch SpinAppExecutor")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// SpinAppExecutor has been requested for deletion
	if !executor.DeletionTimestamp.IsZero() {
		err := r.handleDeletion(ctx, &executor)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.removeFinalizer(ctx, &executor)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Make sure the finalizer is present
	err := r.ensureFinalizer(ctx, &executor)
	return ctrl.Result{}, client.IgnoreNotFound(err)
}

// handleDeletion makes sure no SpinApps are dependent on the SpinAppExecutor
// before allowing it to be deleted.
func (r *SpinAppExecutorReconciler) handleDeletion(ctx context.Context, executor *spinv1alpha1.SpinAppExecutor) error {
	log := logging.FromContext(ctx)

	var spinApps spinv1alpha1.SpinAppList
	if err := r.Client.List(ctx, &spinApps, client.InNamespace(executor.Namespace), client.MatchingFields{spinAppExecutorKey: executor.Name}); err != nil {
		log.Error(err, "Unable to list SpinApps")
		return err
	}

	if len(spinApps.Items) > 0 {
		r.Recorder.Event(executor, "Warning", "DeletionBlocked", "Cannot delete SpinAppExecutor with dependent SpinApps")
		return errors.New("cannot delete SpinAppExecutor with dependent SpinApps")
	}

	return nil
}

// removeFinalizer removes the finalizer from a SpinAppExecutor.
func (r *SpinAppExecutorReconciler) removeFinalizer(ctx context.Context, executor *spinv1alpha1.SpinAppExecutor) error {
	if controllerutil.ContainsFinalizer(executor, SpinOperatorFinalizer) {
		controllerutil.RemoveFinalizer(executor, SpinOperatorFinalizer)
		if err := r.Client.Update(ctx, executor); err != nil {
			return err
		}
	}
	return nil
}

// ensureFinalizer ensures the finalizer is present on a SpinAppExecutor.
func (r *SpinAppExecutorReconciler) ensureFinalizer(ctx context.Context, executor *spinv1alpha1.SpinAppExecutor) error {
	if !controllerutil.ContainsFinalizer(executor, SpinOperatorFinalizer) {
		controllerutil.AddFinalizer(executor, SpinOperatorFinalizer)
		if err := r.Client.Update(ctx, executor); err != nil {
			return err
		}
	}
	return nil
}
