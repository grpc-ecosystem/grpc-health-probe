FROM golang:1.24.6 AS build
ENV PROJECT grpc_health_probe
WORKDIR /src/$PROJECT
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go install -a -tags netgo -ldflags=-w

FROM alpine:3
COPY --from=build /go/bin/grpc-health-probe /bin/grpc_health_probe
ENTRYPOINT [ "/bin/grpc_health_probe" ]
