# pdf-service

The PDF parsing microservice for [Ogen](https://github.com/ogen-app/ogen) (CON-103).

It exposes a small gRPC API (`pdf.v1.PdfService`) over the **private network only**
— the Ogen API is its sole client. It owns all PDF handling so the API stays a
pure-Go, `CGO_ENABLED=0` static binary with no `poppler-utils`/`ledongthuc`.

## API (`proto/pdf/v1/pdf.proto`)

Both RPCs **client-stream** the PDF bytes (first frame = options, then raw
bytes), and reply once:

- **`Parse`** — content-bank ingestion: per-page text → page-aware **chunks** +
  page count + first-page thumbnail.
- **`Render`** — post attachments: page count + first-page thumbnail (no text).

Plus the standard `grpc.health.v1.Health` service.

A corrupt/encrypted/non-PDF input returns gRPC `InvalidArgument` (terminal — the
client must not retry); transport/internal errors are transient.

## Engine: pdfium

Backed by [`klippa-app/go-pdfium`](https://github.com/klippa-app/go-pdfium) using
its **pure-Go WebAssembly (wazero)** backend — `pdfium.wasm` is embedded in the
binary, so there's nothing external to install and the build is
`CGO_ENABLED=0` static.

> **Switching to native CGO** (faster, but needs `libpdfium` + a C toolchain):
> swap `webassembly.Init` for `single_threaded.Init`/`multi_threaded.Init` in
> [`internal/pdfengine/engine.go`](internal/pdfengine/engine.go) and build with
> `CGO_ENABLED=1` + libpdfium on the linker path (adjust the Dockerfile). The
> service and gRPC layer are backend-agnostic.

## Configuration

| Env | Default | Meaning |
|---|---|---|
| `PDF_SERVICE_LISTEN` | `:50051` | gRPC listen address |
| `PDF_SERVICE_WORKERS` | `4` | max concurrent pdfium instances (pdfium is single-threaded) |

## Develop

```sh
buf generate proto      # regenerate gen/ from the proto
go test ./...           # unit + end-to-end gRPC tests (real pdfium)
go run ./cmd/pdf-service # serve on :50051
```

## Deploy

```sh
docker build -t pdf-service .
docker run --rm -p 50051:50051 pdf-service
```

The Ogen API reaches it at `pdf-service.railway.internal:50051` (prod) /
`pdf-service:50051` (compose) via `PDF_SERVICE_ADDR`. The link is plaintext h2c —
it never leaves the private network.

## Contract sync

`proto/pdf/v1/pdf.proto` is the source of truth. The Ogen API consumes a
generated client; evolve the contract **backward-compatibly** (`buf breaking`)
and release the image + client together (see the CON-103 PRD release runbook).
