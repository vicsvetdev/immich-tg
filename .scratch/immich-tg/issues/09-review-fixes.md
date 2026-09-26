# 09 — Fixes from the project review

**What to build:** Three fixes found in the full-project review, before first deployment:

1. **No genuine failure loses its Problem Report because a shutdown started.** Only failures *caused by* the shutdown stay unreported.
2. **An extreme Telegram rate-limit wait can't block the service indefinitely.**
3. **The test suite is fast enough to run routinely with `-race`.**

Background:
- Today, `fail()` in the publisher drops the Problem Report whenever the context is cancelled at report time. That includes a real Telegram rejection or a Share Link failure that happens just before SIGTERM, which then only gets an Info log line. The spec requires a Problem Report and a stdout log for every failure.
- `retry_after` is waited out with no upper bound.
- `go test -race ./...` takes about 4 minutes. 93% of that is building thumbnails from the fake's 1440×810 default preview, in two tests that run 200 polls each.

See the spec: "Outcomes per Video", "Lifecycle" and "Testing Decisions". The review findings are in the orchestrator's review of `a7eecfd..HEAD`.

**Blocked by:** None — can start immediately.

**Status:** done

- [x] **Shutdown classification:** a failure is "caused by shutdown" only when shutdown has begun **and** the error is a context cancellation (`errors.Is(err, context.Canceled)`). Only those are logged as "Video abandoned on shutdown" with no Problem Report.
- [x] **Genuine failures during shutdown are still reported.** Any other failure produces its normal outcome (Link-only Post where the Outcomes table says so, plus the Problem Report). This holds even if shutdown began while the Video was being handled. Those calls are sent on a context that isn't cancelled, with a short limit (5 s, like the stopped message), before the stopped message.
- [x] **Shutdown log noise:** Immich search errors caused by shutdown cancellation are logged at Info, not Error.
- [x] **`retry_after` cap:** a 429 whose `retry_after` is over 5 minutes is not waited out. It is treated as a normal failure of that call, which leads to the usual outcome (Link-only Post and/or Problem Report per the table). The Problem Report's error says the rate-limit wait was too long and includes the value. Waits of 5 minutes or less behave as before.
- [x] **Waits end on shutdown:** a rate-limit wait in progress is interrupted by shutdown.
- [x] **Tests** at the process edge:
  - a genuine Telegram rejection that lands after shutdown has begun still produces its Problem Report before the stopped message
  - a cancellation-caused failure produces none
  - a 429 with `retry_after` over 5 minutes produces the upload-failure outcome with no wait
  - a wait in progress ends when shutdown begins
- [x] **Test speed:**
  - The fake Immich's default preview is tiny (e.g. 32×18). Thumbnail tests that need a large preview set one explicitly.
  - The two memory-bound tests run about 40 polls instead of 200. That is still well past the steady state of about 12 tracked Videos.
  - Harness tests call `t.Parallel()`, except tests that rely on wall-clock timing (`TestPollsRunEveryPollInterval`).
  - Target: `go test -race ./...` finishes in under 60 s on the operator's machine, and `go test ./...` in under 10 s.
- [x] The README stays accurate. Update the Stop section and the 429/Problem Report descriptions if the behaviour text changes.
