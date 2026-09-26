# 06 — Failure outcomes and rate limiting

**What to build:** Every remaining row of the spec's "Outcomes per Video" table behaves as specified, so that no New Video is silently lost while the service is up. Telegram rate limiting (429) is waited out rather than reported.

**Blocked by:** 03 — Posts carry the Transcode as a playable video; 05 — Waiting Videos: dropped silently, or time out.

**Status:** done

- [x] **Oversized:** a playback `Content-Length` over 2,000,000,000 bytes means the response is closed before any upload, then a Link-only Post ("video too large for Telegram") and a Problem Report (kind: oversized).
- [x] **Missing `Content-Length`:** the upload goes ahead, and a Telegram rejection takes the upload-failure path.
- [x] **Transcode download fails, or the header is unreadable** (no `moov` or video track within 1 MiB): a Link-only Post ("video not available in Telegram") and a Problem Report.
- [x] **Telegram upload fails (not 429):** a Link-only Post ("video not available in Telegram") and a Problem Report.
- [x] **Share Link creation fails:** a Problem Report only, no Post.
- [x] **Link-only fallback itself fails:** a single Problem Report noting both failures.
- [x] **Telegram unreachable, even for the Problem Report:** the failure is logged to stdout, and the service keeps running.
- [x] **Two Telegram error shapes handled:**
  - JSON: `{"ok":false,"error_code","description"}`
  - a bare HTTP status with an empty body (e.g. 413, 400, 501)
- [x] **429 handling:**
  - A 429 with `parameters.retry_after` waits that long and repeats the call. This is not a Problem Report.
  - For `sendVideo`, the repeat reopens a fresh Transcode stream from Immich.
- [x] **Tests** cover every row above, including a 429 on upload followed by success (a single Post, with the full Transcode bytes).
