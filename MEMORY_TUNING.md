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
bounded), so the "leak" seen in metrics is reclaimable memory the Go
runtime/cgroup hasn't returned — a flat plateau after a burst, not a rising
staircase. The knobs below make RSS track real usage.

## What actually holds memory here

Two different pools, and the knobs only reach one of them:

- **Go heap (these knobs reach it):** `server.go` reassembles the entire streamed
  PDF in memory (up to `maxPDFBytes`, 110 MB) before the engine runs, plus — for
  `Parse` — every page's extracted text, the chunk slices, and the thumbnail PNG.
  All request-local, all garbage once the RPC returns. This is a real, sometimes
  large, high-water mark that `GOMEMLIMIT`/`GOGC`/the idle scavenge do control.
- **pdfium native heap (these knobs DO NOT reach it):** pdfium runs in-process via
  **CGO**, so its per-document working memory is native `malloc` — outside the Go
  heap. `GOMEMLIMIT` doesn't count it and `debug.FreeOSMemory` doesn't return it.
  pdfium frees it back to the C allocator on instance/document close, but glibc
  may keep it as its own high-water mark (Go has no `malloc_trim` hook). This is a
  separate ceiling; the only levers on it are input size and concurrency.

## Recommended values

```bash
PDF_SERVICE_SCAVENGE_ON_IDLE=true    # returns the Go-side burst plateau
PDF_SERVICE_GC_PERCENT=50            # keep default; minor lever here
PDF_SERVICE_MEMORY_LIMIT_RATIO=0.7   # safety net, tune to your cap (see below)
```

### `SCAVENGE_ON_IDLE=true` — returns the Go-side plateau

Railway bills measured RSS over time, and the service is idle most hours. The
idle scavenge (`debug.FreeOSMemory` once all in-flight `Extract`/`Render` calls
drain) drops the **Go-side** burst plateau — the reassembled PDF bytes and
extracted text, which for a large document is the bigger of the two pools — back
toward the Go baseline, so most billing samples catch the low idle number. It
does **not** return pdfium's native plateau; if idle RSS stays high after this is
on, that residue is the C allocator's, not Go's. Keep it on regardless — the
Go-side reclaim is free money on a bursty/idle service.

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
  bursts. If it's still high idle, decide which pool it is — Go baseline
  (always-on gRPC/runtime, only architectural levers left) vs. pdfium native
  residue (input/concurrency levers above), which the scavenge can't touch.

## Verify after deploy

Run a couple of requests and confirm memory returns toward baseline (reuse)
rather than climbing across bursts (which would be a real leak). At
`LOG_LEVEL=debug`, each scavenge logs `component=pdfengine.scavenge`; boot logs
the derived `GOMEMLIMIT` under `component=runtimetune`.
