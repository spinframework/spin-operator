package controller

import (
	"context"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sync"
	"testing"
	"time"

	spinv1alpha1 "github.com/spinkube/spin-operator/api/v1alpha1"
	"github.com/spinkube/spin-operator/internal/generics"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

type envTestState struct {
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	scheme    *runtime.Scheme
}

// SetupEnvTest will start a fake kubernetes and client for use when testing
// reconciliation loops that require a kubernetes api.
func SetupEnvTest(t *testing.T) *envTestState {
	t.Helper()

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without calling the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.28.3-%s-%s", goruntime.GOOS, goruntime.GOARCH)),
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Skipf("envtest unavailable: %v", err)
	}

	require.NoError(t, err)
	require.NotNil(t, cfg)

	scheme := runtime.NewScheme()
	require.NoError(t, clientscheme.AddToScheme(scheme))

	err = spinv1alpha1.AddToScheme(scheme)
	require.NoError(t, err)

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	require.NotNil(t, k8sClient)

	t.Cleanup(func() {
		err := testEnv.Stop()
		require.NoError(t, err)
	})

	return &envTestState{
		cfg:       cfg,
		k8sClient: k8sClient,
		testEnv:   testEnv,
		scheme:    scheme,
	}
}

func setupController(t *testing.T) (*envTestState, ctrl.Manager, *SpinAppReconciler) {
	t.Helper()

	envTest := SetupEnvTest(t)

	opts := zap.Options{
		Development: true,
	}
	logger := zap.New(zap.UseFlagOptions(&opts))

	// B/c of https://github.com/kubernetes-sigs/controller-runtime/issues/2937
	skipNameValidation := true

	mgr, err := ctrl.NewManager(envTest.cfg, manager.Options{
		Metrics: metricsserver.Options{BindAddress: "0"},
		Scheme:  envTest.scheme,
		// Provide a real logger to controllers - this means that when tests fail we
		// get to see the controller logs that lead to the failure - if we decide this
		// is too noisy then we can gate this behind an env var like SPINKUBE_TEST_LOGS.
		Logger:     logger,
		Controller: controllerconfig.Controller{SkipNameValidation: &skipNameValidation},
	})

	require.NoError(t, err)

	ctrlr := &SpinAppReconciler{
		Client:   envTest.k8sClient,
		Scheme:   envTest.scheme,
		Recorder: mgr.GetEventRecorderFor("spinapp-reconciler"),
	}

	require.NoError(t, ctrlr.SetupWithManager(mgr))

	return envTest, mgr, ctrlr
}

func TestReconcile_Integration_StartupShutdown(t *testing.T) {
	t.Parallel()

	_, mgr, _ := setupController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()
	require.NoError(t, mgr.Start(ctx))
}

func TestReconcile_Integration_Deployment_Respects_Executor_Config(t *testing.T) {
	t.Parallel()

	envTest, mgr, _ := setupController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		require.NoError(t, mgr.Start(ctx))
		wg.Done()
	}()

	// Create an executor that creates a deployment with a given runtimeClassName
	executor := &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "executor",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName: generics.Ptr("a-runtime-class"),
			},
		},
	}

	require.NoError(t, envTest.k8sClient.Create(ctx, executor))

	spinApp := &spinv1alpha1.SpinApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppSpec{
			Executor: "executor",
			Image:    "ghcr.io/radu-matei/perftest:v1",
		},
	}

	// Create an app that uses the executor
	require.NoError(t, envTest.k8sClient.Create(ctx, spinApp))

	// Wait for the underlying deployment to exist
	var deployment appsv1.Deployment
	require.Eventually(t, func() bool {
		err := envTest.k8sClient.Get(ctx,
			types.NamespacedName{
				Namespace: "default",
				Name:      "app"},
			&deployment)
		return err == nil
	}, 3*time.Second, 100*time.Millisecond)

	require.Equal(t, "a-runtime-class", *deployment.Spec.Template.Spec.RuntimeClassName)

	// Terminate the context to force the manager to shut down.
	cancelFunc()
	wg.Wait()
}

func TestReconcile_Integration_RuntimeConfig(t *testing.T) {
	t.Parallel()

	envTest, mgr, _ := setupController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		require.NoError(t, mgr.Start(ctx))
		wg.Done()
	}()

	// Create an executor that creates a deployment with a given runtimeClassName
	executor := &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "executor",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName: generics.Ptr("a-runtime-class"),
			},
		},
	}

	require.NoError(t, envTest.k8sClient.Create(ctx, executor))

	spinApp := &spinv1alpha1.SpinApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppSpec{
			Executor: "executor",
			Image:    "ghcr.io/radu-matei/perftest:v1",
			RuntimeConfig: spinv1alpha1.RuntimeConfig{
				KeyValueStores: []spinv1alpha1.KeyValueStoreConfig{
					{
						Name: "default",
						Type: "redis",
						Options: []spinv1alpha1.RuntimeConfigOption{
							{
								Name:  "url",
								Value: "redis://localhost:9000",
							},
						},
					},
				},
			},
		},
	}

	// Create an app that uses the executor
	require.NoError(t, envTest.k8sClient.Create(ctx, spinApp))

	// Wait for the underlying deployment to exist
	var deployment appsv1.Deployment
	require.Eventually(t, func() bool {
		err := envTest.k8sClient.Get(ctx,
			types.NamespacedName{
				Namespace: "default",
				Name:      "app"},
			&deployment)
		return err == nil
	}, 3*time.Second, 100*time.Millisecond)

	var runtimeConfigVolume corev1.Volume
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name == "spin-runtime-config" {
			runtimeConfigVolume = volume
		}
	}
	require.NotNil(t, runtimeConfigVolume.VolumeSource.Secret, "expected the deployment to have a runtime config")

	var rcSecret corev1.Secret
	require.NoError(t, envTest.k8sClient.Get(ctx, types.NamespacedName{
		Name:      runtimeConfigVolume.VolumeSource.Secret.SecretName,
		Namespace: "default"}, &rcSecret))

	expected := `[[config_provider]]
type = 'env'
prefix = 'SPIN_VARIABLE_'

[key_value_store]
[key_value_store.default]
type = 'redis'
url = 'redis://localhost:9000'
`
	require.Equal(t, expected, string(rcSecret.Data["runtime-config.toml"]))

	// Terminate the context to force the manager to shut down.
	cancelFunc()
	wg.Wait()
}

func TestReconcile_Integration_RuntimeConfig_SecretAlreadyExists(t *testing.T) {
	t.Parallel()

	envTest, mgr, _ := setupController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		require.NoError(t, mgr.Start(ctx))
		wg.Done()
	}()

	// Create an executor that creates a deployment with a given runtimeClassName
	executor := &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "executor",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName: generics.Ptr("a-runtime-class"),
			},
		},
	}

	require.NoError(t, envTest.k8sClient.Create(ctx, executor))

	spinApp := &spinv1alpha1.SpinApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppSpec{
			Executor: "executor",
			Image:    "ghcr.io/radu-matei/perftest:v1",
			RuntimeConfig: spinv1alpha1.RuntimeConfig{
				KeyValueStores: []spinv1alpha1.KeyValueStoreConfig{
					{
						Name: "default",
						Type: "redis",
						Options: []spinv1alpha1.RuntimeConfigOption{
							{
								Name:  "url",
								Value: "redis://localhost:9000",
							},
						},
					},
				},
			},
		},
	}

	// Create an app that uses the executor
	require.NoError(t, envTest.k8sClient.Create(ctx, spinApp))

	// Wait for the underlying deployment to exist
	var deployment appsv1.Deployment
	require.Eventually(t, func() bool {
		err := envTest.k8sClient.Get(ctx,
			types.NamespacedName{
				Namespace: "default",
				Name:      "app"},
			&deployment)
		return err == nil
	}, 3*time.Second, 100*time.Millisecond)

	var runtimeConfigVolume corev1.Volume
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.Name == "spin-runtime-config" {
			runtimeConfigVolume = volume
		}
	}
	require.NotNil(t, runtimeConfigVolume.VolumeSource.Secret, "expected the deployment to have a runtime config")

	var rcSecret corev1.Secret
	require.NoError(t, envTest.k8sClient.Get(ctx, types.NamespacedName{
		Name:      runtimeConfigVolume.VolumeSource.Secret.SecretName,
		Namespace: "default"}, &rcSecret))

	//update the spinapp
	require.NoError(t, envTest.k8sClient.Get(ctx,
		types.NamespacedName{
			Namespace: "default",
			Name:      "app"},
		spinApp), "fetch spinapp to update")

	spinApp.Spec.Image = "ghcr.io/radu-matei/updated-image:v2"
	require.NoError(t, envTest.k8sClient.Update(ctx, spinApp))

	// Wait for the underlying deployment to exist and have updated image
	require.Eventually(t, func() bool {
		err := envTest.k8sClient.Get(ctx,
			types.NamespacedName{
				Namespace: "default",
				Name:      "app"},
			&deployment)
		return err == nil && deployment.Spec.Template.Spec.Containers[0].Image == "ghcr.io/radu-matei/updated-image:v2"
	}, 3*time.Second, 100*time.Millisecond)

	// Terminate the context to force the manager to shut down.
	cancelFunc()
	wg.Wait()
}

func TestConstructDeployment_MinimalApp(t *testing.T) {
	t.Parallel()

	app := minimalSpinApp()

	cfg := &spinv1alpha1.ExecutorDeploymentConfig{
		RuntimeClassName: generics.Ptr("bananarama"),
	}
	deployment, err := constructDeployment(context.Background(), app, cfg, "", "", nil)
	require.NoError(t, err)
	require.NotNil(t, deployment)

	require.Equal(t, generics.Ptr(int32(1)), deployment.Spec.Replicas)
	require.Len(t, deployment.Spec.Template.Spec.Containers, 1)
	require.Equal(t, app.Spec.Image, deployment.Spec.Template.Spec.Containers[0].Image)
	require.Equal(t, generics.Ptr("bananarama"), deployment.Spec.Template.Spec.RuntimeClassName)
}

func TestConstructDeployment_WithPodLabels(t *testing.T) {
	t.Parallel()

	key, value := "dev.spinkube.tests", "foo"
	app := spinAppWithLabels(map[string]string{
		key: value,
	})

	cfg := &spinv1alpha1.ExecutorDeploymentConfig{
		RuntimeClassName: generics.Ptr("bananarama"),
	}
	deployment, err := constructDeployment(context.Background(), app, cfg, "", "", nil)
	require.NoError(t, err)
	require.NotNil(t, deployment)

	require.Equal(t, generics.Ptr(int32(1)), deployment.Spec.Replicas)
	require.Len(t, deployment.Spec.Template.Labels, 3)
	require.Equal(t, deployment.Spec.Template.Labels[key], value)
}

func TestReconcile_Integration_AnnotationAndLabelPropagation(t *testing.T) {
	t.Parallel()

	envTest, mgr, _ := setupController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		require.NoError(t, mgr.Start(ctx))
		wg.Done()
	}()

	// Create an executor that creates a deployment with a given runtimeClassName
	executor := &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "executor",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName: generics.Ptr("a-runtime-class"),
			},
		},
	}

	require.NoError(t, envTest.k8sClient.Create(ctx, executor))

	spinApp := &spinv1alpha1.SpinApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppSpec{
			Executor: "executor",
			Image:    "ghcr.io/radu-matei/perftest:v1",
			PodLabels: map[string]string{
				"my.pod.label": "value",
			},
			PodAnnotations: map[string]string{
				"my.pod.annotation": "value",
			},
			DeploymentAnnotations: map[string]string{
				"my.deployment.annotation": "value",
			},
		},
	}

	require.NoError(t, envTest.k8sClient.Create(ctx, spinApp))

	// Wait for the underlying deployment to exist
	var deployment appsv1.Deployment
	require.Eventually(t, func() bool {
		err := envTest.k8sClient.Get(ctx,
			types.NamespacedName{
				Namespace: "default",
				Name:      "app"},
			&deployment)
		return err == nil
	}, 3*time.Second, 100*time.Millisecond)

	require.Equal(t, "a-runtime-class", *deployment.Spec.Template.Spec.RuntimeClassName)
	require.Equal(t,
		map[string]string{
			"core.spinkube.dev/app-name":       "app",
			"core.spinkube.dev/app.app.status": "ready",
			"my.pod.label":                     "value",
		},
		deployment.Spec.Template.ObjectMeta.Labels)
	require.Equal(t, map[string]string{"my.pod.annotation": "value"}, deployment.Spec.Template.ObjectMeta.Annotations)
	require.Equal(t, map[string]string{"my.deployment.annotation": "value"}, deployment.ObjectMeta.Annotations)

	// Terminate the context to force the manager to shut down.
	cancelFunc()
	wg.Wait()
}

func TestReconcile_Integration_Deployment_SpinCAInjection(t *testing.T) {
	t.Parallel()

	envTest, mgr, _ := setupController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		require.NoError(t, mgr.Start(ctx))
		wg.Done()
	}()

	// Create an executor that creates a deployment with the default Spin CA Secret
	executorWithDefaults := &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "executor-default",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName:      generics.Ptr("foobar"),
				InstallDefaultCACerts: true,
			},
		},
	}

	// Create an executor that creates a deployment with a custom secret that doesn't
	// exist in the cluster
	executorWithCustomNonExistentSecret := &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "executor-custom-name",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName:      generics.Ptr("foobar"),
				CACertSecret:          "my-custom-secret-name",
				InstallDefaultCACerts: true,
			},
		},
	}

	require.NoError(t, envTest.k8sClient.Create(ctx, executorWithDefaults))
	require.NoError(t, envTest.k8sClient.Create(ctx, executorWithCustomNonExistentSecret))

	for _, executor := range []*spinv1alpha1.SpinAppExecutor{
		executorWithDefaults,
		executorWithCustomNonExistentSecret,
	} {
		spinApp := &spinv1alpha1.SpinApp{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "app-" + executor.Name,
				Namespace: "default",
			},
			Spec: spinv1alpha1.SpinAppSpec{
				Executor: executor.Name,
				Image:    "ghcr.io/radu-matei/perftest:v1",
			},
		}
		require.NoError(t, envTest.k8sClient.Create(ctx, spinApp))

		var deployment appsv1.Deployment
		require.Eventually(t, func() bool {
			err := envTest.k8sClient.Get(ctx,
				types.NamespacedName{
					Namespace: "default",
					Name:      spinApp.Name},
				&deployment)
			return err == nil
		}, 3*time.Second, 100*time.Millisecond)

		expectedSecretName := "spin-ca"
		if executor.Spec.DeploymentConfig.CACertSecret != "" {
			expectedSecretName = executor.Spec.DeploymentConfig.CACertSecret
		}

		// ensure the secret exists
		var secret corev1.Secret
		require.Eventually(t, func() bool {
			err := envTest.k8sClient.Get(ctx,
				types.NamespacedName{
					Namespace: "default",
					Name:      expectedSecretName,
				},
				&secret)
			return err == nil
		}, 3*time.Second, 100*time.Millisecond)

		if !slices.ContainsFunc(deployment.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool {
			return v.Name == "spin-ca" && v.VolumeSource.Secret.SecretName == expectedSecretName
		}) {
			t.Errorf("deployment does not contain a binding to the correct CA Secret")
		}

		if !slices.ContainsFunc(deployment.Spec.Template.Spec.Containers, func(c corev1.Container) bool {
			return slices.ContainsFunc(c.VolumeMounts, func(v corev1.VolumeMount) bool {
				return v.Name == "spin-ca"
			})
		}) {
			t.Fatalf("deployment does not include ca-bundle mount")
		}
	}
	// Terminate the context to force the manager to shut down.
	cancelFunc()
	wg.Wait()
}

func TestReconcile_Integration_WorkloadIdentity_Azure(t *testing.T) {
	t.Parallel()

	envTest, mgr, _ := setupController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		require.NoError(t, mgr.Start(ctx))
		wg.Done()
	}()

	// Create an executor that creates a deployment
	executor := &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "executor",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName: generics.Ptr("a-runtime-class"),
			},
		},
	}

	require.NoError(t, envTest.k8sClient.Create(ctx, executor))

	spinApp := &spinv1alpha1.SpinApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppSpec{
			Executor: "executor",
			Image:    "ghcr.io/radu-matei/perftest:v1",
			WorkloadIdentity: &spinv1alpha1.WorkloadIdentity{
				ServiceAccountName: "custom-sa",
				ProviderMetadata: map[string]string{
					"azure": "true",
				},
			},
		},
	}

	require.NoError(t, envTest.k8sClient.Create(ctx, spinApp))

	// Wait for the underlying deployment to exist
	var deployment appsv1.Deployment
	require.Eventually(t, func() bool {
		err := envTest.k8sClient.Get(ctx,
			types.NamespacedName{
				Namespace: "default",
				Name:      "app"},
			&deployment)
		return err == nil
	}, 3*time.Second, 100*time.Millisecond)

	// Verify Azure workload identity label is set
	require.Equal(t, "true", deployment.Spec.Template.ObjectMeta.Labels["azure.workload.identity/use"])
	// Verify service account name is set
	require.Equal(t, "custom-sa", deployment.Spec.Template.Spec.ServiceAccountName)

	// Terminate the context to force the manager to shut down.
	cancelFunc()
	wg.Wait()
}
