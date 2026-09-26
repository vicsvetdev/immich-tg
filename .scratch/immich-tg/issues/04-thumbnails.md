# 04 — Thumbnails on video Posts

**What to build:** Every video Post shows a proper preview thumbnail in Telegram, made from Immich's preview image. If a thumbnail can't be made, the Post still goes out without one, with no Problem Report.

See the spec: "Transcode handling → Thumbnail".

**Blocked by:** 03 — Posts carry the Transcode as a playable video.

**Status:** ready-for-agent

- [ ] **Source:** the preview is fetched via `GET /api/assets/{id}/thumbnail?size=preview` and decoded according to `Content-Type` (JPEG or WebP).
- [ ] **Scaling:** it is scaled so the longest side is at most 320 px, keeping aspect ratio.
- [ ] **Encoding:** it is encoded as JPEG, lowering quality until it is under 204,800 bytes.
- [ ] **Attachment:** it is sent with `sendVideo` as `thumbnail=attach://thumb` plus a multipart file part named `thumb`.
- [ ] **Failure is non-fatal:** a failed fetch, decode or encode still produces a normal video Post without a thumbnail, and nothing goes to the Log Channel.
- [ ] **Tests** cover:
  - a JPEG preview
  - a WebP preview
  - an oversized preview being scaled down
  - a failing thumbnail endpoint
