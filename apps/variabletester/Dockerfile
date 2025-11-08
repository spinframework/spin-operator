FROM --platform=${BUILDPLATFORM} tinygo/tinygo:0.39.0 AS build
WORKDIR /opt/build

COPY go.mod go.sum .

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

COPY . .

RUN tinygo build -target=wasip1 -gc=leaking -buildmode=c-shared -no-debug -o main.wasm main.go

FROM scratch
COPY --from=build /opt/build/main.wasm .
COPY --from=build /opt/build/spin.toml .
