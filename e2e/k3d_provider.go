package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/e2e-framework/klient/conf"
	"sigs.k8s.io/e2e-framework/pkg/utils"
)

const k3dImage = "ghcr.io/spinframework/containerd-shim-spin/k3d:v0.23.0"

var k3dBin = "k3d"

type Cluster struct {
	name        string
	kubecfgFile string
	restConfig  *rest.Config
}

func (c *Cluster) Create(context.Context, string) (string, error) {
	if err := findOrInstallK3d(); err != nil {
		return "", fmt.Errorf("k3d: failed to find or install k3d: %w", err)
	}

	if _, ok := clusterExists(c.name); ok {
		klog.V(4).Info("Skipping k3d Cluster creation. Cluster already created ", c.name)
		return c.GetKubeconfig()
	}

	command := fmt.Sprintf("%s cluster create %s --image %s -p '8081:80@loadbalancer' --agents 2 --wait", k3dBin, c.name, k3dImage)
	if useNativeSnapshotter() {
		command = command + ` --k3s-arg "--snapshotter=native@agent:0,1;server:0"`
	}
	klog.V(4).Info("Launching:", command)
	p := utils.RunCommand(command)
	if p.Err() != nil {
		outBytes, err := io.ReadAll(p.Out())
		if err != nil {
			klog.ErrorS(err, "failed to read data from the k3d create process output due to an error")
		}
		return "", fmt.Errorf("k3d: failed to create cluster %q: %w: %s: %s", c.name, p.Err(), p.Result(), string(outBytes))
	}

	clusters, ok := clusterExists(c.name)
	if !ok {
		return "", fmt.Errorf("k3d Cluster.Create: cluster %v still not in 'cluster list' after creation: %v", c.name, clusters)
	}
	klog.V(4).Info("k3d cluster available: ", clusters)

	kConfig, err := c.GetKubeconfig()
	if err != nil {
		return "", err
	}
	return kConfig, c.initKubernetesAccessClients()
}

func (c *Cluster) Destroy() error {
	p := utils.RunCommand(fmt.Sprintf(`%s cluster delete %s`, k3dBin, c.name))
	if p.Err() != nil {
		return fmt.Errorf("%s cluster delete: %w", k3dBin, p.Err())
	}

	return nil
}

func (c *Cluster) GetKubeconfig() (string, error) {
	kubecfg := fmt.Sprintf("%s-kubecfg", c.name)

	p := utils.RunCommand(fmt.Sprintf(`%s kubeconfig get %s`, k3dBin, c.name))

	if p.Err() != nil {
		return "", fmt.Errorf("k3d get kubeconfig: %w", p.Err())
	}

	var stdout bytes.Buffer
	if _, err := stdout.ReadFrom(p.Out()); err != nil {
		return "", fmt.Errorf("k3d kubeconfig stdout bytes: %w", err)
	}

	file, err := os.CreateTemp("", fmt.Sprintf("k3d-cluster-%s", kubecfg))
	if err != nil {
		return "", fmt.Errorf("k3d kubeconfig file: %w", err)
	}
	defer file.Close()

	c.kubecfgFile = file.Name()

	if n, err := io.Copy(file, &stdout); n == 0 || err != nil {
		return "", fmt.Errorf("k3d kubecfg file: bytes copied: %d: %w]", n, err)
	}

	return file.Name(), nil
}

func (c *Cluster) initKubernetesAccessClients() error {
	cfg, err := conf.New(c.kubecfgFile)
	if err != nil {
		return err
	}
	c.restConfig = cfg

	return nil
}

func findOrInstallK3d() error {
	_, err := utils.FindOrInstallGoBasedProvider(k3dBin, k3dBin, "github.com/k3d-io/k3d/v5", "v5.6.3")
	return err
}

func clusterExists(name string) (string, bool) {
	clusters := utils.FetchCommandOutput(fmt.Sprintf("%s cluster list --no-headers", k3dBin))
	for _, c := range strings.Split(clusters, "\n") {
		cl := strings.Split(c, " ")[0]
		if cl == name {
			return clusters, true
		}
	}
	return clusters, false
}

func useNativeSnapshotter() bool {
	return os.Getenv("E2E_USE_NATIVE_SNAPSHOTTER") == "true" // nolint:forbidigo
}
