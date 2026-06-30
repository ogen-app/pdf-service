# syntax=docker/dockerfile:1
# pdf-service: the gRPC PDF parser (CON-103).
#
# Pure-Go build: pdfium ships as an embedded WebAssembly module (the
# klippa-app/go-pdfium webassembly backend, run on wazero), so this is a
# CGO_ENABLED=0 static binary with NO system libpdfium.
#
# To switch to the native CGO backend (faster, heavier): build with
# CGO_ENABLED=1 + a C toolchain, install libpdfium (bblanchon/pdfium-binaries)
# into the runtime image on the linker path, and swap webassembly.Init for
# single_threaded.Init in internal/pdfengine/engine.go. Nothing else changes.

# ─── build ───────────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /pdf-service ./cmd/pdf-service

# ─── runtime ─────────────────────────────────────────────────────────────────
FROM alpine:3.20
ARG GRPC_HEALTH_PROBE_VERSION=v0.4.34
RUN apk add --no-cache ca-certificates wget && \
    wget -qO /usr/local/bin/grpc_health_probe \
      "https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/${GRPC_HEALTH_PROBE_VERSION}/grpc_health_probe-linux-amd64" && \
    chmod +x /usr/local/bin/grpc_health_probe && \
    apk del wget && \
    addgroup -S app && adduser -S -G app app

COPY --from=build /pdf-service /usr/local/bin/pdf-service
USER app

ENV PDF_SERVICE_LISTEN=":50051"
EXPOSE 50051

# Private-network only — orchestrators probe gRPC health via grpc_health_probe.
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
  CMD ["/usr/local/bin/grpc_health_probe", "-addr=:50051"]

ENTRYPOINT ["/usr/local/bin/pdf-service"]
