# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/snapnotify .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && mkdir -p /config/sounds && chown -R 65532:65532 /config
COPY --from=build /out/snapnotify /usr/local/bin/snapnotify
USER 65532:65532
WORKDIR /config
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD ["/usr/local/bin/snapnotify", "-healthcheck", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/usr/local/bin/snapnotify"]
CMD ["-config", "/config/config.yaml"]
