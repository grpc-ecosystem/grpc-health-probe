# One Dockerfile for both ways the image gets built. BINARY_FROM selects where
# the binary comes from:
#   build     (default) compile it from the source tree -- `docker build .`;
#   prebuilt  copy GoReleaser's cross-compiled binary from its context, which
#             holds one per platform as <os>/<arch>[/<variant>]/grpc_health_probe
#             -- .goreleaser.yml (dockers_v2.build_args) on a release.
# BuildKit builds only the stages the chosen path references, so a release
# build pulls no golang image.
ARG BINARY_FROM=build

# Builds with the newer of the image's Go and go.mod's toolchain line, which
# Go downloads if needed: the image's default GOTOOLCHAIN=local would ignore
# go.mod. git lets go build stamp the commit and module version from .git.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.24@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build
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

# The static binary alone: root CAs are embedded (see main.go) and the user is
# numeric, so there is no base image. The path is the one ko used, which
# `COPY --from=ghcr.io/grpc-ecosystem/grpc-health-probe` in the README relies on.
FROM scratch
COPY --from=bin /grpc_health_probe /ko-app/grpc-health-probe
USER 65532:65532
ENTRYPOINT ["/ko-app/grpc-health-probe"]
