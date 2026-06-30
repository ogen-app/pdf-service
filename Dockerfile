# syntax=docker/dockerfile:1
# pdf-service: native-CGO build (CON-103). Links bblanchon's prebuilt libpdfium
# (glibc), discovered at build time via pkg-config and loaded at runtime from
# /usr/local/lib. linux/amd64 only.

# ─── build ───────────────────────────────────────────────────────────────────
FROM golang:1.26-bookworm AS build
WORKDIR /app

# Prebuilt libpdfium (lib + headers) exposed through pkg-config.
RUN set -eux; \
    mkdir -p /opt/pdfium; \
    curl -fsSL https://github.com/bblanchon/pdfium-binaries/releases/latest/download/pdfium-linux-x64.tgz \
      | tar xz -C /opt/pdfium; \
    printf 'prefix=/opt/pdfium\nlibdir=/opt/pdfium/lib\nincludedir=/opt/pdfium/include\n\nName: PDFium\nDescription: PDFium\nVersion: 1\nLibs: -L${libdir} -lpdfium\nCflags: -I${includedir}\n' \
      > /usr/lib/pkgconfig/pdfium.pc

COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=1
RUN go build -trimpath -ldflags="-s -w" -o /pdf-service ./cmd/pdf-service

# ─── runtime ─────────────────────────────────────────────────────────────────
# Debian (glibc) — bblanchon's libpdfium is glibc-built, so not Alpine/musl.
FROM debian:bookworm-slim
ARG GRPC_HEALTH_PROBE_VERSION=v0.4.34
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates wget; \
    wget -qO /usr/local/bin/grpc_health_probe \
      "https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/${GRPC_HEALTH_PROBE_VERSION}/grpc_health_probe-linux-amd64"; \
    chmod +x /usr/local/bin/grpc_health_probe; \
    apt-get purge -y wget; apt-get autoremove -y; rm -rf /var/lib/apt/lists/*; \
    useradd -r -u 10001 app

COPY --from=build /opt/pdfium/lib/libpdfium.so /usr/local/lib/libpdfium.so
COPY --from=build /pdf-service /usr/local/bin/pdf-service
RUN ldconfig
USER app

ENV PDF_SERVICE_LISTEN=":50051"
EXPOSE 50051

# Private-network only — orchestrators probe gRPC health via grpc_health_probe.
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
  CMD ["/usr/local/bin/grpc_health_probe", "-addr=:50051"]

ENTRYPOINT ["/usr/local/bin/pdf-service"]
