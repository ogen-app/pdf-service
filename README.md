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

## Engine: pdfium (native, CGO)

Backed by [`klippa-app/go-pdfium`](https://github.com/klippa-app/go-pdfium) with
its **native CGO** backend (`single_threaded`), linking
[bblanchon's prebuilt `libpdfium`](https://github.com/bblanchon/pdfium-binaries).
pdfium is not thread-safe, so the single-threaded pool serialises pdfium calls;
the gRPC server handles concurrency above it.

**Build prerequisites:** a C toolchain, `CGO_ENABLED=1`, and `libpdfium`
discoverable via `pkg-config` (a `pdfium.pc`). The Dockerfile and the `test`
workflow fetch the prebuilt library and write the `.pc` automatically — see
*Develop* for a local one-time setup.

> For higher parallelism, swap `single_threaded.Init` for `multi_threaded.Init`
> (needs a worker subprocess) in
> [`internal/pdfengine/engine.go`](internal/pdfengine/engine.go).

## Configuration

| Env | Default | Meaning |
|---|---|---|
| `PDF_SERVICE_LISTEN` | `:50051` | gRPC listen address |
| `PDF_SERVICE_WORKERS` | `4` | max concurrent pdfium instances (pdfium is single-threaded) |
| `PDF_SERVICE_GC_PERCENT` | `50` | GC target (GOGC); lower = smaller Go heap, more CPU. `<=0` keeps the runtime default |
| `PDF_SERVICE_MEMORY_LIMIT_RATIO` | `0.9` | soft mem limit (GOMEMLIMIT) as a fraction of the cgroup limit; ignored if `GOMEMLIMIT` is set or no cgroup limit is found |
| `PDF_SERVICE_SCAVENGE_ON_IDLE` | `true` | return freed Go-heap memory to the OS once all in-flight work drains after a burst |
| `LOG_LEVEL` | `info` | slog minimum level: `debug` \| `info` \| `warn` \| `error` |
| `LOG_FORMAT` | `json` | slog handler: `json` (prod) \| `text` (local) |

### Memory footprint

Every request reassembles the whole streamed PDF in memory (up to 110 MB) and,
for `Parse`, holds every page's extracted text — all request-local and garbage
once the RPC returns. A freshly booted idle container sits at ~8 MB.

The bigger idle cost is native: pdfium runs **in-process via CGO**, and its
working memory is glibc `malloc` spread across per-thread arenas (Go runs CGO
calls on whichever OS thread is free). When a document closes, pdfium frees that
memory but glibc keeps the pages resident, so without intervention RSS ratchets
up a step with every burst while the live Go heap stays at a few MB — it looks
like a leak but isn't.

The idle scavenge (once all in-flight `Extract`/`Render` calls drain) returns
both pools to the OS: `debug.FreeOSMemory` for the Go heap and `malloc_trim(0)`
for the glibc arenas. The Dockerfile also caps glibc at two arenas
(`GLIBC_TUNABLES=glibc.malloc.arena_max=2`), which bounds the native footprint
during bursts. `GOMEMLIMIT` (derived from the cgroup limit, in
`internal/runtimetune`) and `GOGC` govern only the Go heap. See
`MEMORY_TUNING.md`.

Logging is structured `log/slog` (CON-107). Every log line carries a
`component`; each RPC also gets one access-log line and, when the caller sends an
`x-request-id` metadata header (else one is generated and echoed back), a
`request_id` — plus `tenant_id` from `x-tenant-id` — so a request correlates
across the API, its jobs, and this service.

## Develop

The CGO build needs `libpdfium` + `pkg-config`. One-time local setup (macOS x64):

```sh
mkdir -p /tmp/pdfium
curl -sL https://github.com/bblanchon/pdfium-binaries/releases/latest/download/pdfium-mac-x64.tgz | tar xz -C /tmp/pdfium
# bblanchon's dylib ships with a relative install name — make it absolute:
install_name_tool -id /tmp/pdfium/lib/libpdfium.dylib /tmp/pdfium/lib/libpdfium.dylib
cat > /tmp/pdfium/pdfium.pc <<'EOF'
prefix=/tmp/pdfium
libdir=/tmp/pdfium/lib
includedir=/tmp/pdfium/include
Name: PDFium
Version: 1
Libs: -L${libdir} -lpdfium
Cflags: -I${includedir}
EOF
export PKG_CONFIG_PATH=/tmp/pdfium CGO_ENABLED=1
# Linux: use pdfium-linux-x64.tgz and `export LD_LIBRARY_PATH=/tmp/pdfium/lib`.
```

```sh
buf generate proto       # regenerate gen/ from the proto
go test ./...            # unit + end-to-end gRPC tests (real native pdfium)
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
