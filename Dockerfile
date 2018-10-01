FROM golang:1.10 AS build

ENV PROJECT github.com/grpc-ecosystem/grpc_health_probe
WORKDIR /go/src/$PROJECT
COPY . .
RUN go get -u github.com/golang/dep/cmd/dep && \
    dep ensure -vendor-only -v
RUN go install -a -tags netgo -ldflags "-linkmode external -extldflags -static"

FROM alpine:latest
COPY --from=build /go/bin/grpc_health_probe /bin/grpc_health_probe
ENTRYPOINT [ "/bin/grpc_health_probe" ]
