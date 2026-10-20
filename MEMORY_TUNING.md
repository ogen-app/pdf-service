# Memory tuning (Railway)

How to set the runtime-memory knobs for a **lightly-loaded** `pdf-service` on
Railway where **memory consumption translates directly to budget**.

## Billing model that drives these choices

Railway bills **actual measured usage** (GB-hours of real RSS + vCPU-hours), not
the plan cap or any limit you configure. A lightly-loaded service is idle for the
vast majority of its hours, so the **idle baseline dominates the bill** — not the
rare burst peak. The goal is therefore to keep idle RSS low, not just to cap the
peak.

The service holds no state across requests (every buffer is request-local and
bounded), so the "leak" seen in metrics is reclaimable memory the allocators
haven't returned. Without the native trim (below) it even looks like a rising
staircase: measured locally, five identical bursts took idle RSS 120 → 246 MB
while the live Go heap stayed at 3–4 MB. The knobs below make RSS track real
usage.

## What actually holds memory here

Two different pools, reached by different knobs:

- **Go heap (these knobs reach it):** `server.go` reassembles the entire streamed
  PDF in memory (up to `maxPDFBytes`, 110 MB) before the engine runs, plus — for
  `Parse` — every page's extracted text, the chunk slices, and the thumbnail PNG.
  All request-local, all garbage once the RPC returns. This is a real, sometimes
  large, high-water mark that `GOMEMLIMIT`/`GOGC`/the idle scavenge do control.
- **pdfium native heap (the idle scavenge's `malloc_trim` reaches it):** pdfium
  runs in-process via **CGO**, so its per-document working memory is native
  `malloc` — outside the Go heap, so `GOMEMLIMIT`/`GOGC` don't see it. Go runs CGO
  calls on whichever OS thread is free, and glibc gives each thread its own
  arena (64 MB-aligned anonymous mappings in `/proc/<pid>/smaps`). pdfium frees
  its memory on document close, but glibc keeps the pages resident in those
  arenas — this was **most of the idle RSS** (≈95% after a few bursts). Two
  fixes are in place: the idle scavenge calls `malloc_trim(0)` (via CGO,
  `internal/pdfengine/malloctrim_linux.go`), and the Dockerfile sets
  `GLIBC_TUNABLES=glibc.malloc.arena_max=2` to bound how many arenas a burst can
  spread across. Peak native usage during a burst is still driven by input size
  and concurrency.

## Recommended values

```bash
PDF_SERVICE_SCAVENGE_ON_IDLE=true    # returns the burst plateau (Go + native)
PDF_SERVICE_GC_PERCENT=50            # keep default; minor lever here
PDF_SERVICE_MEMORY_LIMIT_RATIO=0.7   # safety net, tune to your cap (see below)
```

### `SCAVENGE_ON_IDLE=true` — returns the burst plateau

Railway bills measured RSS over time, and the service is idle most hours. Once
all in-flight `Extract`/`Render` calls drain, the idle scavenge runs
`debug.FreeOSMemory` (the Go heap: reassembled PDF bytes, extracted text) and
then `malloc_trim(0)` (pdfium's freed native memory in glibc's arenas), so most
billing samples catch the low idle number. Measured locally over five identical
bursts, idle RSS stayed flat at ~55 MB with it, against 120 → 246 MB and still
rising without the trim, with no measurable throughput cost. Keep it on.

### `GC_PERCENT=50` — minor lever, don't overthink

GOGC only affects the Go heap high-water mark **during activity**. The
reassembled-PDF buffer is a short-lived spike and the idle scavenge force-returns
it anyway, so GOGC has **little effect on idle RSS**. 50 gives slightly lower
burst peaks than the default 100 at trivial CPU cost. Don't go below ~40 — that
just burns CPU (also billed) chasing an already-small heap, and it does nothing
for the pdfium native pool.

### `MEMORY_LIMIT_RATIO` — a safety net, not a cost lever

This sets `GOMEMLIMIT`, a soft **ceiling** that prevents Go-heap OOM; it does not
reduce average usage, so it doesn't directly cut the bill. Key nuance:
**`GOMEMLIMIT` governs only the Go heap — not pdfium's native allocations**,
which also count against the container cgroup. Leave room for them:

| Your Railway memory limit | Suggested ratio | Reasoning |
|---|---|---|
| ≤ 1 GB | **0.6–0.7** | reserve room for pdfium's native per-document memory so a big PDF doesn't OOM the container |
| ≥ 4 GB (or unset) | 0.85–0.9 | native headroom is ample; ratio barely binds |

If you haven't set an explicit memory limit on the Railway service, `memory.max`
is likely your plan's large default, so `0.9 × huge` is effectively unlimited and
the ratio does nothing. In that case **set an explicit Railway memory limit** —
it costs nothing extra on usage-based billing, gives a hard backstop against a
runaway, and makes the derived `GOMEMLIMIT` meaningful.

## Two bigger levers

- **Input size:** the single largest driver of peak RSS is one big PDF — the
  reassembled buffer (Go, ≤ 110 MB via `maxPDFBytes`) *plus* pdfium's native cost
  to open and render it. Lowering `maxPDFBytes` in `server.go` (if the API's real
  upload cap is smaller) tightens both at once, more directly than any GC knob.
- **Concurrency:** pdfium *extraction* serialises through the single-threaded
  pool, but `server.go` reassembles each inbound stream's bytes **before** it
  queues on the engine — so N concurrent uploads can each hold up to 110 MB of Go
  buffer at once. If burst RSS is driven by concurrent requests rather than one
  big file, the lever is capping inbound concurrency at the gRPC server, not the
  engine's advisory `PDF_SERVICE_WORKERS`.
- **Idle baseline itself:** after deploy, check what the service floors at between
  bursts (~8 MB fresh, ~55 MB after bursts in local tests). If it's still high,
  look at `/proc/<pid>/smaps`: many large 64 MB-aligned anonymous regions mean
  glibc arenas (check the scavenge ran); a big Go heap in `GODEBUG=gctrace=1`
  output after a `(forced)` GC means Go-side retention.

## Verify after deploy

Run a couple of requests and confirm memory returns toward baseline rather than
climbing across bursts. With the trim in place, a staircase that survives idle
scavenges would be a real leak. At
`LOG_LEVEL=debug`, each scavenge logs `component=pdfengine.scavenge`; boot logs
the derived `GOMEMLIMIT` under `component=runtimetune`.
