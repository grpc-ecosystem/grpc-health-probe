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
$ grpc_health_probe -addr=localhost:9999 -connect-timeout 250ms -rpc-timeout 100ms
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

## Using the gRPC Health Checking Protocol

To make use of the `grpc_health_probe`, your application must implement the
[gRPC Health Checking Protocol v1][hc]. This means you must to register the
`Health` service and implement the `rpc Check` that returns a `SERVING` status.

Since the Health Checking protocol is part of the gRPC core, it has
packages/libraries available for the languages supported by gRPC:

[[health.proto](https://github.com/grpc/grpc/blob/master/src/proto/grpc/health/v1/health.proto)]
[[Go](https://godoc.org/google.golang.org/grpc/health/grpc_health_v1)]
[[Java](https://github.com/grpc/grpc-java/blob/master/services/src/generated/main/grpc/io/grpc/health/v1/HealthGrpc.java)]
[[Python](https://github.com/grpc/grpc/tree/master/src/python/grpcio_health_checking)]
[[C#](https://github.com/grpc/grpc/tree/master/src/csharp/Grpc.HealthCheck)/[NuGet](https://www.nuget.org/packages/Grpc.HealthCheck/)]
[[Ruby](https://www.rubydoc.info/gems/grpc/Grpc/Health/Checker)] ...

Most of the languages listed above provide helper functions that hides
implementation details. This eliminates the need for you to implement the
`Check` rpc yourself.

## Example: gRPC health checking on Kubernetes

You are recommended to use [Kubernetes exec probes][execprobe] and define
liveness and readiness checks for your gRPC server pods.

You can bundle the statically compiled `grpc_health_probe` in your container
image. Choose a [binary release][rel] and download it in your Dockerfile:

```
RUN GRPC_HEALTH_PROBE_VERSION=v0.3.1 && \
    wget -qO/bin/grpc_health_probe https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/${GRPC_HEALTH_PROBE_VERSION}/grpc_health_probe-linux-amd64 && \
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

## Health Checking TLS Servers

If a gRPC server is serving traffic over TLS, or uses TLS client authentication
to authorize clients, you can still use `grpc_health_probe` to check health
with command-line options:

| Option | Description |
|:------------|-------------|
| **`-tls`** | use TLS (default: false) |
| **`-tls-ca-cert`** | path to file containing CA certificates (to override system root CAs) |
| **`-tls-client-cert`** | client certificate for authenticating to the server |
| **`-tls-client-key`** | private key for for authenticating to the server |
| **`-tls-no-verify`** | use TLS, but do not verify the certificate presented by the server (INSECURE) (default: false) |
| **`-tls-server-name`** | override the hostname used to verify the server certificate |

## Other Available Flags

| Option | Description |
|:------------|-------------|
| **`-v`**    | verbose logs (default: false) |
| **`-connect-timeout`** | timeout for establishing connection |
| **`-rpc-timeout`** | timeout for health check rpc |
| **`-user-agent`** | user-agent header value of health check requests (default: grpc_health_probe) |
| **`-service`** | service name to check (default: "") - empty string is convention for server health |

**Example:**

1. Start the `route_guide` [example
   server](https://github.com/grpc/grpc-go/tree/be59908d40f00be3573a50284c3863f1a37b8528/examples/route_guide)
   with TLS by running:

       go run server/server.go -tls

2. Run `grpc_client_probe` with the [CA
   certificate](https://github.com/grpc/grpc-go/blob/be59908d40f00be3573a50284c3863f1a37b8528/testdata/ca.pem)
   (in the `testdata/` directory) and hostname override the
   [cert](https://github.com/grpc/grpc-go/blob/be59908d40f00be3573a50284c3863f1a37b8528/testdata/server1.pem) is signed for:

      ```sh
      $ grpc_health_probe -addr 127.0.0.1:10000 \
          -tls \
          -tls-ca-cert /path/to/testdata/ca.pem \
          -tls-server-name=x.test.youtube.com

      status: SERVING
      ```

## Exit codes

It is not recommended to rely on specific exit statuses. Any failure will be
a non-zero exit code.

| Exit Code | Description |
|:-----------:|-------------|
| **0** | success: rpc response is `SERVING`. |
| **1** | failure: invalid command-line arguments |
| **2** | failure: connection failed or timed out |
| **3** | failure: rpc failed or timed out |
| **4** | failure: rpc successful, but the response is not `SERVING` |

----
This is not an official Google project.

[hc]: https://github.com/grpc/grpc/blob/master/doc/health-checking.md
[k8s]: https://kubernetes.io/
[execprobe]: https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-probes/#define-a-liveness-command
[rel]: https://github.com/grpc-ecosystem/grpc-health-probe/releases
