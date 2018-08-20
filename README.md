# grpc_health_probe(1)

The `grpc_health_probe` utility allows you to query health of gRPC services that
expose service their status through the [gRPC Health Checking Protocol][hc].

This command-line utility makes a RPC to `/grpc.health.v1.Health/Check`. If it
responds with a `SERVING` status, the `grpc_health_probe` will exit with
success, otherwise it will exit with a non-zero exit code (documented below).

`grpc_health_probe` is meant to be used for health checking gRPC applications in
[Kubernetes][k8s], using the [exec probes][execprobe].

**EXAMPLES**

```text
$ grpc_health_probe -addr=localhost:5000
healthy: SERVING
```

```text
$ grpc_health_probe -addr=localhost:9999 -conn-timeout 250ms -rpc-timeout 100ms
failed to connect service at "localhost:9999": context deadline exceeded
exit status 2
```

## Installation

**It is recommended** to use a version-stamped binary distribution:
- Refer to the [Releases][rel] section for binary distributions.

Installing from source (not recommended)

- Make sure you have `git` and `go` installed.
- Run: `go get github.com/grpc-ecosystem/grpc-health-probe`
- This will compile the binary into your `$GOPATH/bin` (or `$HOME/go/bin`).

## Implementing the gRPC Health Checking Protocol

To make use of the `grpc_health_probe`, your application must implement the
[gRPC Health Checking Protocol v1][hc]. This means you must to register the
`Health` service and implement the `rpc Check` that returns a `SERVING` status.

Since the Health Checking protocol is part of the gRPC core, it has packages/libraries
available for the languages supported by gRPC:

[[health.proto](https://github.com/grpc/grpc/blob/master/src/proto/grpc/health/v1/health.proto)]
[[Go](https://godoc.org/google.golang.org/grpc/health/grpc_health_v1)]
[[Java](https://github.com/grpc/grpc-java/blob/master/services/src/generated/main/grpc/io/grpc/health/v1/HealthGrpc.java)]
[[Python](https://github.com/grpc/grpc/tree/master/src/python/grpcio_health_checking)]
[[C#](https://github.com/grpc/grpc/tree/master/src/csharp/Grpc.HealthCheck)/[NuGet](https://www.nuget.org/packages/Grpc.HealthCheck/)]
[[Ruby](https://www.rubydoc.info/gems/grpc/Grpc/Health/Checker)] ...

Most of the languages listed above provide helper functions that hides
impementation details. This eliminates the need for you to implement the `Check`
rpc yourself.

## Exit codes

It is not recommended to rely on specific exit statuses other than zero versus
non-zero.

| Exit Code | Description |
|:-----------:|-------------|
| **0** | success: rpc response is `SERVING`. |
| **1** | failure: invalid command-line arguments |
| **2** | failure: connection failed or timed out |
| **3** | failure: rpc failed or timed out |
| **4** | failure: rpc successful, but the response is not `SERVING` |

## Example: gRPC health checking on Kubernetes

You are recommended to use [Kubernetes exec probes][execprobe] and define
liveness and readiness checks for your gRPC server pods.

You can bundle the statically compiled `grpc_health_probe` in your container
image. Choose a [binary release][rel] and download it in your Dockerfile:

```
RUN GRPC_HEALTH_PROBE_VERSION=0.1.0 && \
    wget -qO/bin/grpc_health_probe https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/v${GRPC_HEALTH_PROBE_VERSION}/grpc_health_probe-linux-amd64 &&
    chmod +x /bin/grpc_health_probe
```

In your Kubernetes Pod specification manifest, specify a `livenessProbe` and/or
`readinessProbe` for the container:

```yaml
spec:
  containers:
  - name: server
    image: "[YOUR-DOCKER-IMAGE]"
    ports:
    - containerPort: 5000
    readinessProbe:
      exec:
        command: ["/bin/grpc_health_probe", "-addr=:5000"]
      initialDelaySeconds: 5
    livenessProbe:
      exec:
        command: ["/bin/grpc_health_probe", "-addr=:5000"]
      initialDelaySeconds: 10
```

This approach provide proper readiness/liveness checking to your applications
that implement the [gRPC Health Checking Protocol][hc].

----

This is not an official Google project.

[hc]: https://github.com/grpc/grpc/blob/master/doc/health-checking.md
[k8s]: https://kubernetes.io/
[execprobe]: https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-probes/#define-a-liveness-command
[rel]: https://github.com/grpc-ecosystem/grpc-health-probe/releases
