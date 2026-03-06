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
	"sync"
	"testing"
	"time"

	spinv1alpha1 "github.com/spinframework/spin-operator/api/v1alpha1"
	"github.com/spinframework/spin-operator/internal/generics"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	controllerconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func setupExecutorController(t *testing.T) (*envTestState, ctrl.Manager, *SpinAppExecutorReconciler) {
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

	ctrlr := &SpinAppExecutorReconciler{
		Client:   envTest.k8sClient,
		Scheme:   envTest.scheme,
		Recorder: mgr.GetEventRecorderFor("spinappexecutor-reconciler"),
	}

	require.NoError(t, ctrlr.SetupWithManager(mgr))

	return envTest, mgr, ctrlr
}
func TestSpinAppExecutorReconcile_StartupShutdown(t *testing.T) {
	t.Parallel()

	_, mgr, _ := setupExecutorController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()
	require.NoError(t, mgr.Start(ctx))
}

func TestSpinAppExecutorReconcile_ContainerDShimSpinExecutorCreate(t *testing.T) {
	t.Parallel()

	envTest, mgr, _ := setupExecutorController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		require.NoError(t, mgr.Start(ctx))
		wg.Done()
	}()

	executor := testContainerdShimSpinExecutor()
	list := &spinv1alpha1.SpinAppExecutorList{}
	require.NoError(t, envTest.k8sClient.List(ctx, list))
	require.True(t, len(list.Items) == 0)
	require.NoError(t, envTest.k8sClient.Create(ctx, executor))
	require.NoError(t, envTest.k8sClient.List(ctx, list))
	require.True(t, len(list.Items) == 1)
}

func TestSpinAppExecutorReconcile_ContainerDShimSpinExecutorDelete(t *testing.T) {
	t.Parallel()

	envTest, mgr, _ := setupExecutorController(t)
	executor := testContainerdShimSpinExecutor()

	envTest.k8sClient = fake.NewClientBuilder().WithScheme(envTest.scheme).WithObjects(
		executor,
	).Build()

	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		require.NoError(t, mgr.Start(ctx))
		wg.Done()
	}()

	list := &spinv1alpha1.SpinAppExecutorList{}
	require.NoError(t, envTest.k8sClient.List(ctx, list))
	require.True(t, len(list.Items) == 1)
	require.NoError(t, envTest.k8sClient.Delete(ctx, executor))
	require.NoError(t, envTest.k8sClient.List(ctx, list))
	require.True(t, len(list.Items) == 0)
}

func testContainerdShimSpinExecutor() *spinv1alpha1.SpinAppExecutor {
	return &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-executor",
			Namespace: "default",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName: generics.Ptr("test-runtime"),
			},
		},
	}
}

func TestSpinAppExecutorReconcile_CrossNamespaceDoesNotBlockDeletion(t *testing.T) {
	t.Parallel()

	envTest, mgr, _ := setupExecutorController(t)

	ctx, cancelFunc := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFunc()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		require.NoError(t, mgr.Start(ctx))
		wg.Done()
	}()

	// Create two namespaces
	nsA := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns-a"}}
	nsB := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns-b"}}
	require.NoError(t, envTest.k8sClient.Create(ctx, nsA))
	require.NoError(t, envTest.k8sClient.Create(ctx, nsB))

	// Create an executor with the same name in both namespaces
	executorA := &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "containerd-shim-spin",
			Namespace: "test-ns-a",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName: generics.Ptr("wasmtime-spin-v2"),
			},
		},
	}
	executorB := &spinv1alpha1.SpinAppExecutor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "containerd-shim-spin",
			Namespace: "test-ns-b",
		},
		Spec: spinv1alpha1.SpinAppExecutorSpec{
			CreateDeployment: true,
			DeploymentConfig: &spinv1alpha1.ExecutorDeploymentConfig{
				RuntimeClassName: generics.Ptr("wasmtime-spin-v2"),
			},
		},
	}
	require.NoError(t, envTest.k8sClient.Create(ctx, executorA))
	require.NoError(t, envTest.k8sClient.Create(ctx, executorB))

	// Wait for finalizers to be added by the controller
	require.Eventually(t, func() bool {
		var exec spinv1alpha1.SpinAppExecutor
		if err := envTest.k8sClient.Get(ctx, client.ObjectKeyFromObject(executorA), &exec); err != nil {
			return false
		}
		return len(exec.Finalizers) > 0
	}, 5*time.Second, 100*time.Millisecond, "executor A should have finalizer")

	require.Eventually(t, func() bool {
		var exec spinv1alpha1.SpinAppExecutor
		if err := envTest.k8sClient.Get(ctx, client.ObjectKeyFromObject(executorB), &exec); err != nil {
			return false
		}
		return len(exec.Finalizers) > 0
	}, 5*time.Second, 100*time.Millisecond, "executor B should have finalizer")

	// Create a SpinApp in namespace B that references the executor by name
	spinApp := &spinv1alpha1.SpinApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "test-ns-b",
		},
		Spec: spinv1alpha1.SpinAppSpec{
			Executor: "containerd-shim-spin",
			Image:    "ghcr.io/spinkube/containerd-shim-spin/examples/spin-rust-hello:v0.15.1",
			Replicas: 1,
		},
	}
	require.NoError(t, envTest.k8sClient.Create(ctx, spinApp))

	// Delete executor in namespace A — this should succeed because
	// the SpinApp is in namespace B, not namespace A.
	require.NoError(t, envTest.k8sClient.Delete(ctx, executorA))

	// The executor in namespace A should be fully deleted (finalizer removed)
	require.Eventually(t, func() bool {
		var exec spinv1alpha1.SpinAppExecutor
		err := envTest.k8sClient.Get(ctx, client.ObjectKeyFromObject(executorA), &exec)
		return err != nil // NotFound means it was deleted
	}, 5*time.Second, 100*time.Millisecond, "executor A should be deleted — SpinApp in namespace B should not block it")

	// Verify executor in namespace B still exists (it has a dependent SpinApp)
	var execB spinv1alpha1.SpinAppExecutor
	require.NoError(t, envTest.k8sClient.Get(ctx, client.ObjectKeyFromObject(executorB), &execB))
}
