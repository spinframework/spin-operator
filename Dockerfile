# syntax=docker/dockerfile:1

# Build the manager binary
FROM --platform=${BUILDPLATFORM} golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.sum ./

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY . .

# Don't set a fallback value for TARGETARCH so that it defaults to the GOARCH default
# equivalent to `BUILDPLATFORM` - this ensures that `docker build .` will build for
# the users local arch.
RUN --mount=type=cache,target=/root/.cache/go-build \
 CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} make golangci-build

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/bin/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
