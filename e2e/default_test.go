package e2e

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	spinapps_v1alpha1 "github.com/spinframework/spin-operator/api/v1alpha1"
)

var runtimeClassName = "wasmtime-spin-v2"

// TestDefaultSetup is a test that checks that the minimal setup works
// with the containerd wasm shim runtime as the default runtime.
func TestDefaultSetup(t *testing.T) {
	var client klient.Client

	helloWorldImage := "ghcr.io/spinframework/containerd-shim-spin/examples/spin-rust-hello:latest"
	testSpinAppName := "test-spinapp"

	defaultTest := features.New("default and most minimal setup").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client = cfg.Client()

			testSpinApp := newSpinAppCR(testSpinAppName, helloWorldImage, "containerd-shim-spin", nil)
			if err := client.Resources().Create(ctx, testSpinApp); err != nil {
				t.Fatalf("Failed to create spinapp: %s", err)
			}

			return ctx
		}).
		Assess("spin app deployment is created and available",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				if err := wait.For(
					conditions.New(client.Resources()).DeploymentAvailable(testSpinAppName, testNamespace),
					wait.WithTimeout(3*time.Minute),
					wait.WithInterval(time.Second),
				); err != nil {
					t.Fatal(err)
				}

				return ctx
			}).
		Assess("spin app service is created and available", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			svc := &corev1.ServiceList{
				Items: []corev1.Service{
					{ObjectMeta: metav1.ObjectMeta{Name: testSpinAppName, Namespace: testNamespace}},
				},
			}

			if err := wait.For(
				conditions.New(client.Resources()).ResourcesFound(svc),
				wait.WithTimeout(3*time.Minute),
				wait.WithInterval(time.Second),
			); err != nil {
				t.Fatal(err)
			}
			return ctx
		}).
		Assess("spin app status contains deployment and service names", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var app spinapps_v1alpha1.SpinApp
			if err := client.Resources().Get(ctx, testSpinAppName, testNamespace, &app); err != nil {
				t.Fatalf("Failed to get spinapp: %s", err)
			}

			// Get the deployment by label
			var deploymentList appsv1.DeploymentList
			if err := client.Resources(testNamespace).List(ctx, &deploymentList, resources.WithLabelSelector("core.spinkube.dev/app-name="+testSpinAppName)); err != nil {
				t.Fatalf("Failed to list deployments: %s", err)
			}
			if len(deploymentList.Items) != 1 {
				t.Fatalf("Expected 1 deployment, got %d", len(deploymentList.Items))
			}
			deployment := deploymentList.Items[0]

			if app.Status.DeploymentName != deployment.Name {
				t.Errorf("Expected status.deploymentName to be %s, got %s", deployment.Name, app.Status.DeploymentName)
			}

			// Get the service by label
			var serviceList corev1.ServiceList
			if err := client.Resources(testNamespace).List(ctx, &serviceList, resources.WithLabelSelector("core.spinkube.dev/app-name="+testSpinAppName)); err != nil {
				t.Fatalf("Failed to list services: %s", err)
			}
			if len(serviceList.Items) != 1 {
				t.Fatalf("Expected 1 service, got %d", len(serviceList.Items))
			}
			service := serviceList.Items[0]

			if app.Status.ServiceName != service.Name {
				t.Errorf("Expected status.serviceName to be %s, got %s", service.Name, app.Status.ServiceName)
			}

			return ctx
		}).
		Feature()
	testEnv.Test(t, defaultTest)
}

func newSpinAppCR(name, image, executor string, components []string) *spinapps_v1alpha1.SpinApp {
	app := spinapps_v1alpha1.SpinApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: spinapps_v1alpha1.SpinAppSpec{
			Replicas: 1,
			Image:    image,
			Executor: executor,
		},
	}
	if components != nil {
		app.Spec.Components = components
	}
	return &app
}
