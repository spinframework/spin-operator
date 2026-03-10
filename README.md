# Spin Operator

Spin Operator enables deploying Spin applications to Kubernetes. It watches [SpinApp Custom
Resources](https://www.spinkube.dev/docs/glossary/#spinapp-crd) and realizes the desired state in
the Kubernetes cluster.

This project was built using the Kubebuilder framework and contains a Spin App CRD and controller.

All documentation is available online at https://www.spinkube.dev/docs/. If you're just getting
started, the [quickstart guide](https://www.spinkube.dev/docs/install/quickstart/) will guide you to
a minimal installation that'll work while you walk through the introduction.

To get more help:

- Join the #spinkube channel on Slack at https://cncf.slack.com.

To contribute to SpinKube, check out the [contributing
guide](https://www.spinkube.dev/docs/contrib/) for information about getting involved.

## Running the test suite

To run the test suite, execute the following command:

```shell
make test
```

## Building

To build the Spin Operator binary, execute the following command:

```shell
make
```

## Running a local development environment

There are two options to run spin-operator:

1. Run spin-operator on your computer
1. Deploy spin-operator to a remote Kubernetes cluster

### Option 1: Run spin-operator on your computer

k3d is a lightweight Kubernetes distribution that runs on Docker. This is the standard development
workflow most spin-operator developers use to test their changes.

Ensure that your system has all the prerequisites installed before continuing:

- [Go](https://go.dev/)
- [Docker](https://docs.docker.com/engine/install/)
- [k3d](https://k3d.io/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)

Create a k3d cluster:

```shell
k3d cluster create wasm-cluster \
    --image ghcr.io/spinframework/containerd-shim-spin/k3d:v0.23.0 \
    -p "8081:80@loadbalancer" \
    --agents 2
```

Install the `SpinApp` and `SpinAppExecutor` Custom Resource Definitions into the cluster:

```shell
make install
```

Create a `RuntimeClass` and `SpinAppExecutor`:

```shell
kubectl apply -f config/samples/spin-runtime-class.yaml
kubectl apply -f config/samples/spin-shim-executor.yaml
```

Run spin-operator:

```shell
make run
```

Run the sample application:

```shell
kubectl apply -f ./config/samples/simple.yaml
```

Forward a local port to the application so that it can be reached:

```shell
kubectl port-forward svc/simple-spinapp 8083:80
```

In a different terminal window, make a request to the application:

```shell
curl localhost:8083/hello
```

You should see "Hello world from Spin!".

### Option 2: Deploy spin-operator to a remote Kubernetes cluster

This is harder than running Spin Operator on your computer, but deploying Spin Operator into a
remote cluster lets you test things like webhook support.

Ensure that your system has all the prerequisites installed before continuing:

- [Go](https://go.dev/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Docker](https://docs.docker.com/engine/install/) (optional: for building and pushing your own
  Docker image)

Install cert-manager into your cluster for webhook support:

```shell
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.3/cert-manager.yaml
kubectl wait --for=condition=available --timeout=300s deployment/cert-manager-webhook -n cert-manager
```

Install the `SpinApp` and `SpinAppExecutor` Custom Resource Definitions into the cluster:

```shell
make install
```

Create a `RuntimeClass` and `SpinAppExecutor`:

```shell
kubectl apply -f config/samples/spin-runtime-class.yaml
kubectl apply -f config/samples/spin-shim-executor.yaml
```

> OPTIONAL: You can build and push the Spin Operator image using `make docker-build` and
> `make docker-push`.
>
>     export IMG_REPO=<some-registry>/spin-operator
>     make docker-build docker-push

Deploy Spin Operator to the cluster:

```shell
make deploy
```

Run the sample application:

```shell
kubectl apply -f ./config/samples/simple.yaml
```

Forward a local port to the application so that it can be reached:

```shell
kubectl port-forward svc/simple-spinapp 8083:80
```

In a different terminal window, make a request to the application:

```shell
curl localhost:8083/hello
```

You should see "Hello world from Spin!".

> NOTE: If you encounter RBAC errors, you may need to grant yourself cluster-admin privileges or be
> logged in as admin.
