package webhook

import (
	"context"

	spinv1alpha1 "github.com/spinkube/spin-operator/api/v1alpha1"
	"github.com/spinkube/spin-operator/internal/logging"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// nolint:lll
//+kubebuilder:webhook:path=/mutate-core-spinkube-dev-v1alpha1-spinappexecutor,mutating=true,failurePolicy=fail,sideEffects=None,groups=core.spinkube.dev,resources=spinappexecutors,verbs=create;update,versions=v1alpha1,name=mspinappexecutor.kb.io,admissionReviewVersions=v1

// SpinAppExecutorDefaulter mutates SpinApps
type SpinAppExecutorDefaulter struct {
	Client client.Client
}

// Default implements webhook.Defaulter
func (d *SpinAppExecutorDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	log := logging.FromContext(ctx)

	executor := obj.(*spinv1alpha1.SpinAppExecutor)
	log.Info("default", "name", executor.Name)

	return nil
}
