FROM golang:1.24.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOPROXY=off go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/tunnel-server .

FROM alpine:3.22
RUN mkdir -p /data
COPY --from=build /out/tunnel-server /usr/local/bin/tunnel-server
ENV LISTEN_ADDR=:8787 \
    PUBLIC_PORTS=443,8443,1080,8081 \
    TLS_DIR=/data
EXPOSE 8787 443 8443 1080 8081
ENTRYPOINT ["/usr/local/bin/tunnel-server"]
