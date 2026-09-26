# 05 — Waiting Videos: dropped silently, or time out

**What to build:** If the operator trashes, archives or locks a Video before it is posted, nothing appears in either channel.

If a Video stays Waiting longer than `WAIT_TIMEOUT`, because the Transcode never appears, the Video Channel gets a Link-only Post ("video not available in Telegram") and the Log Channel gets a Problem Report. This ticket introduces Problem Reports in their final form.

See the spec: "Detection → Classification", "Post content → Link-only Post" and "Outcomes per Video".

**Blocked by:** 02 — Detect New Videos and post them as text.

**Status:** done

- [x] **Dropped silently:** a Waiting Video missing from the candidate results (trashed, archived, locked or deleted) is dropped, with no message in either channel.
- [x] **Timeout:** a Waiting Video first seen more than `WAIT_TIMEOUT` ago (by the service's clock) produces a Link-only Post via `sendMessage`. The caption is the usual one plus the note "video not available in Telegram", and link previews stay enabled (no `link_preview_options` sent).
- [x] **Problem Report contents:**
  - kind of problem
  - Recording Date
  - `originalFileName`
  - the Immich link `IMMICH_PUBLIC_URL` + `/photos/` + id
  - the underlying error or reason
- [x] **Problem Report delivery:** it goes to the Log Channel, and the same information is logged to stdout.
- [x] **No retries:** a timed-out Video is handled once and never retried.
- [x] **Tests** (with the fake clock) cover:
  - trash, archive and lock while Waiting
  - the timeout producing exactly one Link-only Post and one Problem Report
