FROM entrd-jfrog.ent.nuance.com/docker.io/golang:1.17.8-alpine as builder
ENV PROJECT grpc_health_probe
WORKDIR /src/$PROJECT
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go install -a -tags netgo -ldflags=-w

FROM scratch
WORKDIR /
COPY --from=builder /go/bin/grpc-health-probe /bin/grpc_health_probe
ENTRYPOINT [ "/bin/grpc_health_probe" ]
