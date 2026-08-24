# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/tproxy-server \
    ./cmd/tproxy-server

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 tproxy \
    && adduser -S -D -H -u 10001 -G tproxy tproxy

COPY --from=build /out/tproxy-server /usr/local/bin/tproxy-server

USER 10001:10001
ENTRYPOINT ["/usr/local/bin/tproxy-server"]
CMD ["-config", "/etc/tproxy-server/config.json"]
