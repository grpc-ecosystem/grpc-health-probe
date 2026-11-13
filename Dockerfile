# One Dockerfile for both ways the image gets built. BINARY_FROM selects where
# the binary comes from:
#   build     (default) compile it from the source tree -- `docker build .`;
#   prebuilt  copy GoReleaser's cross-compiled binary from its context, which
#             holds one per platform as <os>/<arch>[/<variant>]/grpc_health_probe
#             -- .goreleaser.yml (dockers_v2.build_args) on a release.
# BuildKit builds only the stages the chosen path references, so a release
# build pulls no golang image.
ARG BINARY_FROM=build

# Root CAs for -tls without -tls-ca-cert. alpine's base image holds the bundle,
# which is the same file on every architecture.
FROM --platform=$BUILDPLATFORM alpine:3.24.4@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf AS certs

# Builds with the newer of the image's Go and go.mod's toolchain line, which
# Go downloads if needed: the image's default GOTOOLCHAIN=local would ignore
# go.mod. git lets go build stamp the commit and module version from .git.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
ENV GOTOOLCHAIN=auto
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH TARGETVARIANT
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -tags=netgo -ldflags="-s -w" -o /grpc_health_probe .

FROM scratch AS prebuilt
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/grpc_health_probe /grpc_health_probe

FROM ${BINARY_FROM} AS bin

# The static binary and the CA bundle, where Go looks for it. The user is
# numeric, so no /etc/passwd is needed. The path and PATH are the ones ko used:
# `COPY --from=ghcr.io/grpc-ecosystem/grpc-health-probe` in the README relies
# on the first, running `grpc-health-probe` by its bare name on the second.
FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=bin /grpc_health_probe /ko-app/grpc-health-probe
ENV PATH=/ko-app
USER 65532:65532
ENTRYPOINT ["/ko-app/grpc-health-probe"]
