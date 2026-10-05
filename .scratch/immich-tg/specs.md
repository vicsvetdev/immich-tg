---
title: immich-tg v1 — forward New Videos from Immich to a Telegram Video Channel
labels: [ready-for-agent]
status: open
---

## Problem Statement

The operator records videos on a Pixel phone, and those videos are backed up automatically to a self-hosted Immich instance. They want the people following a Telegram channel to see each new video soon after it is uploaded, without sharing or forwarding anything by hand.

Telegram's public Bot API rejects uploads over 50 MB, which rules out most phone videos. Original Pixel files can also show up in Telegram as file attachments rather than as playable videos.

The operator wants something small that runs on the homelab alongside Immich, is set up with one `docker-compose.yml` and one `.env`, and needs no babysitting.

## Solution

immich-tg is a small, stateless Go service running in Docker. It regularly asks Immich for Videos the Source User uploaded after the Watch Start. For each New Video it waits until the video is Ready, meaning Immich has produced its 1080p H.264 Transcode. It then publishes one Post to the Video Channel. The Post contains:

- the Transcode as a playable Telegram video
- the Recording Date and the Upload Date
- a Share Link, so viewers can watch or download the original quality in Immich

Link-only Posts cover Videos whose Transcode is over 2 GB, never appears in time, or can't be read or uploaded. Every such problem also goes to a private Log Channel as a Problem Report, together with started/stopped messages that make downtime visible.

Uploads go through a bot and a separate, shared, self-hosted Telegram Bot API server running in `--local` mode, which raises the upload limit to 2000 MB. That server is its own compose project and is out of scope here (ADR 0001).

## User Stories

### Detection and scope

1. As the operator, I want every video I upload to Immich to be posted to the Video Channel automatically, so that I never have to share videos by hand.
2. As the operator, I want only my own uploads (those of the API key owner) to be considered, so that partner-shared or other users' videos never leak into the channel.
3. As the operator, I want the Source User to be derived from the API key, so that I don't have to find and configure a user ID.
4. As the operator, I want videos that existed before the service started to never be posted, so that the first start doesn't flood the channel with my whole library.
5. As the operator, I want old footage I upload today to be posted, so that "new" means newly uploaded, not newly recorded.
6. As the operator, I want motion-photo clips from my Pixel to be ignored, so that the channel only gets real videos.
7. As the operator, I want videos I trash, archive, lock or delete before they are posted to be dropped silently, so that my actions in Immich take precedence.
8. As the operator, I want new uploads detected within about 30 seconds, plus Immich's transcoding time, so that the channel feels timely.
9. As the operator, I want to configure the poll interval, so that I can trade freshness for load.
10. As the operator, I want each Video posted at most once while the service runs, so that viewers never see duplicates.
11. As the operator, I want uploads that land close to a poll boundary to be picked up, so that no New Video is missed.

### Readiness and the Transcode

12. As the operator, I want a Video to be posted only once its Transcode exists, so that the channel always gets a small, playable 1080p H.264 MP4.
13. As the operator, I want the original file never to be uploaded to Telegram, so that nothing arrives as a document or in an unsupported codec.
14. As the operator, I want a Video that stays Waiting longer than a configurable timeout to get a Link-only Post, so that no New Video is silently lost while the service is up.
15. As the operator, I want a Problem Report when that timeout is hit, so that I can investigate Immich's transcoding.
16. As the operator, I want the service to work with Immich's hardware-accelerated transcoding (Quick Sync with hardware decoding), so that I don't have to give up fast transcoding.

### Posts

17. As a viewer, I want each Post to play inline as a Telegram video with the correct aspect ratio, duration and preview thumbnail, so that it looks and plays like a normal video.
18. As a viewer, I want portrait videos to play upright, so that I don't have to turn my phone.
19. As a viewer, I want videos to stream while they download, so that long videos start playing quickly.
20. As a viewer, I want each Post to show the Recording Date in the local time of the place of recording, so that I know when it happened.
21. As a viewer, I want a "Watch in original quality" link in each Post, so that I can play the full-quality file in the browser, and an "Open in Immich" link to the Share Link page, which plays anywhere and offers the download.
22. As a viewer, I want the Share Link to work without an Immich account, a password or an expiry, so that old Posts keep working.
23. ~~As the operator, I want Share Links to hide metadata, so that viewers never see where a video was recorded.~~ Dropped: Immich forbids downloading from a link that hides metadata, and the original carries the location anyway ([ADR 0004](../../docs/adr/0004-share-links-show-metadata.md)).
24. As the operator, I want Share Links to allow downloading, so that viewers can get the original quality.
25. As the operator, I want the caption to omit filenames, locations and camera details, so that Posts stay clean and private.
26. As a viewer, I want an Oversized Video to still appear as a Link-only Post with a short note and a link preview, so that I know it exists, can see what it is, and know why there's no player.
27. As a viewer, I want Posts in the order the videos were uploaded, so that a burst of uploads reads chronologically.
28. As the operator, I want Posts to be fire-and-forget, so that the service needs no memory of past Posts.

### Failures and the Log Channel

29. As the operator, I want a separate Log Channel for Problem Reports, so that the audience never sees errors.
30. As the operator, I want a Transcode that can't be read (download fails, or its header can't be parsed) to produce a Link-only Post and a Problem Report, so that viewers still get the video's link and I learn what broke.
31. As the operator, I want a failed Telegram upload to produce a Link-only Post and a Problem Report, so that viewers still get the video's link and I learn what broke.
32. As the operator, I want a failed Share Link creation to produce a Problem Report and no Post, so that the channel never gets a Post with a dead or missing link.
33. As the operator, I want no automatic retries on failures, so that behaviour stays simple and predictable.
34. As the operator, I want Telegram rate limiting (429) to be waited out rather than treated as a failure, so that upload bursts don't create spurious Problem Reports.
35. As the operator, I want each Problem Report to identify the Video (Recording Date, original filename, a link to it in Immich), the kind of problem and the underlying error, so that I can act on it.
36. As the operator, I want failures that can't be reported to Telegram to at least appear in the container logs, so that `docker logs` always tells the full story.
37. As the operator, I want a "started" message with the Watch Start in the Log Channel, so that I can see when a restart created a gap.
38. As the operator, I want a "stopped" message in the Log Channel on a clean shutdown, so that I can tell planned stops from crashes.

### Startup and configuration

39. As the operator, I want all configuration in one `.env` file, so that setup is one file to fill in.
40. As the operator, I want an `.env.example` that documents every variable, so that I know what to fill in.
41. As the operator, I want separate internal and public Immich URLs, so that API calls and downloads use the LAN while Share Links use my public nginx domain.
42. As the operator, I want the service to check its configuration at startup and exit with a clear message when anything is missing or invalid, so that mistakes surface immediately.
43. As the operator, I want startup to check that Immich is reachable, is a supported version, and that the API key has every permission it needs, naming any missing one, so that I learn about a mis-scoped key at startup, not at the first upload.
44. As the operator, I want startup to check that the bot can post to both channels, so that I learn about missing admin rights at startup.
45. As the operator, I want the container to restart automatically after a failed start or a crash, so that transient homelab hiccups heal on their own.
46. As the operator, I want the API key to need only scoped, minimal permissions, so that a leaked key can do little harm.

### Deployment

47. As the operator, I want a small container image with a single static binary, so that it's lightweight on my homelab.
48. As the operator, I want immich-tg to be a separate compose project from Immich, so that Immich upgrades never touch it.
49. As the operator, I want immich-tg to reach the shared Bot API server over the pre-created `telegram-bot-api` Docker network, so that no Telegram port is exposed.
50. As the operator, I want a README listing the prerequisites and setup order, so that I can deploy it from scratch. The prerequisites are:
    - Immich version and transcoding settings
    - API key scopes
    - bot and channel setup
    - the `logOut` step
    - the external network
    - the Bot API server
51. As the operator, I want graceful shutdown on SIGTERM, so that `docker compose down` is clean and the stopped message goes out.

## Implementation Decisions

### Architecture and language

- Go, with a single static binary.
- Dependencies are kept minimal: the standard library for HTTP, logging and JPEG, plus `golang.org/x/image` for WebP decoding and image scaling.
- One long-running process with no persisted state (ADR 0002). All tracking lives in memory and is lost on restart, which is accepted.
- Everything is processed one Video at a time, oldest upload first.

### Modules

- **Config:** loads and validates the environment variables and applies defaults. Variables:
  - Required: `IMMICH_URL`, `IMMICH_PUBLIC_URL`, `IMMICH_API_KEY`, `TELEGRAM_API_URL`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_VIDEO_CHANNEL_ID`, `TELEGRAM_LOG_CHANNEL_ID`.
  - Optional: `POLL_INTERVAL` (default 30s), `WAIT_TIMEOUT` (default 2h).
- **Immich client:** a thin client for the few endpoints used. It authenticates with the `x-api-key` header, and hides pagination and response shapes behind domain-level operations:
  - server version
  - key permissions
  - who is the Source User
  - find candidate Videos uploaded since X, and which of them are Ready
  - create a Share Link
  - read the Transcode's header
  - open a stream of the Transcode
  - fetch a preview image
- **Telegram client:** a thin client for the Bot API at `TELEGRAM_API_URL`, with domain-level operations:
  - identify the bot
  - check the bot can post to a chat
  - send a video Post (streamed multipart)
  - send a text message

  A 429 is handled inside the client by waiting `parameters.retry_after` seconds and repeating the call. This is the only repeat in the system and does not count as a retry. A `retry_after` over 5 minutes is not waited out: the call fails and takes the normal failure outcome. A wait in progress ends on shutdown. For a video upload, the repeat reopens the Transcode stream from Immich, because the first stream has already been consumed.
- **MP4 header reader:** a minimal parser that reads the Transcode's display dimensions and rotation from the `moov` box. A hand-rolled parser for the few boxes needed is preferred over a library.
- **Thumbnail maker:** turns Immich's preview image into a Telegram thumbnail.
- **Watcher (core loop):** owns the Watch Start, the search window and the in-memory Video list. Each poll:
  1. Discovers New Videos.
  2. Re-checks Waiting Videos for readiness, timeout or dropped state.
  3. Hands Ready or timed-out Videos to the Publisher in upload order.
- **Publisher:** turns one Video into the right outcome: a Post, a Link-only Post and/or a Problem Report. It formats captions and reports.
- **Lifecycle:**
  1. Startup checks.
  2. The started message.
  3. Signal handling.
  4. The stopped message.
  5. Exit codes.
- **Seam requirement:** the whole service can be built from a config plus an injectable clock and a way to trigger a poll, so that tests can drive it without real time passing.

### Supported Immich

- Immich **v3.2.0 or newer**, because the search filter format below was introduced in 3.2. Startup reads the server version and exits with a clear error on anything older.

### Detection (Immich)

- **Source User:** the owner of the API key, from `GET /api/users/me`, which needs `user.read`. Its `id` is the Source User id.
- **Search:** each poll issues two calls to `POST /api/search/metadata` (`asset.read`). Both use the **new filter format**, which must not be mixed with the deprecated flat fields, or Immich returns a 400.
  - **Candidates:**
    ```json
    {
      "filter": {
        "type": {"eq": "VIDEO"},
        "visibility": {"eq": "timeline"},
        "trashedAt": {"eq": null},
        "createdAt": {"gte": "<window start, ISO 8601 UTC>"}
      },
      "size": 1000
    }
    ```
    Pages are followed via `cursor` = the previous response's `assets.nextCursor` until it is null. Items are in `assets.items`.
  - **Ready:** the same body with `"isEncoded": {"eq": true}` added to `filter`.
- **These filter details are mandatory:**
  - v3 search does **not** exclude trashed assets by default, so `trashedAt: {eq: null}` is required.
  - `visibility: timeline` excludes motion-photo clips (`hidden`), archived and locked assets.
  - The filter object is strict, so any unknown key returns a 400.
- **Client-side filtering and sorting:**
  - There is no server-side owner filter. Results include partners' assets, so the service keeps only items whose `ownerId` equals the Source User id.
  - There is no sort by upload time, so the service sorts by `createdAt` itself.
- **Search window start** = max(Watch Start, min(newest `createdAt` seen minus a 5-minute overlap, oldest Waiting Video's `createdAt`)).
  - Before anything has been seen, "newest seen" is the Watch Start.
  - The window start only ever moves forward.
  - The overlap catches late-appearing rows. Deduplication by asset id in the in-memory list prevents double handling.
- **Classification each poll:**
  - A candidate not yet in the list, with `createdAt` ≥ Watch Start, becomes **Waiting**. The service records when it first saw it, using its own clock.
  - A Waiting Video that appears in the Ready results is **Ready**.
  - A Waiting Video missing from the candidate results has been trashed, archived, locked or deleted, so it is **dropped silently**. The window always includes the oldest Waiting Video, so its absence is meaningful.
  - A Waiting Video first seen more than `WAIT_TIMEOUT` ago goes to the **timeout** path.
  - Handled entries with `createdAt` before the window start are forgotten. They can never reappear, so memory stays bounded.
- **Clocks:** the Watch Start comes from the service's clock, and upload times come from Immich's clock. The two are assumed to agree roughly, which holds on the same host or with NTP.

### Immich prerequisites (ADR 0003)

- **Video Transcoding:**
  - Transcode policy "All videos"
  - target resolution 1080p
  - video codec H.264
  - audio codec AAC (the default)

  Any hardware acceleration setting works, because dimensions come from the Transcode itself.
- These settings can't be checked without admin permissions, so they are documented, not checked. If they are wrong, Videos time out and become Link-only Posts with Problem Reports.
- **API key scopes:** `user.read`, `asset.read`, `asset.view`, `asset.share` and `sharedLink.create`. `asset.share` is required for each asset included in a shared link. Download access to originals (`asset.download`) is **not** needed.

### Share Link

- Created just before publishing with `POST /api/shared-links`:
  ```json
  {"type": "INDIVIDUAL", "assetIds": ["<id>"], "allowDownload": true, "showMetadata": true, "allowUpload": false}
  ```
  No `expiresAt`, `password` or `slug`.
- Its URL is `IMMICH_PUBLIC_URL` + `/share/` + the response's `key`.
- If Share Link creation fails, there's a Problem Report and no Post.

### Transcode handling

- **Endpoint:** the Transcode is served by `GET /api/assets/{id}/video/playback` (`asset.view`). For a Ready Video this is always the encoded MP4, because Immich serves the Transcode when one exists and the original otherwise. It supports `Range` requests and sets `Content-Length`.
- **Header read:**
  - A ranged request for the first 1 MiB. Immich always encodes with `-movflags faststart`, so `moov` is at the start.
  - The reader finds the video track (`hdlr` = `vide`), then takes `tkhd` width and height (16.16 fixed point) and the rotation from the `tkhd` matrix.
  - **Display dimensions** = width/height, swapped when the rotation is 90° or 270°. This matters because with Quick Sync plus hardware decoding, Immich passes `-noautorotate`, so portrait Videos are stored as landscape frames plus a rotation matrix. Software fallbacks produce physically rotated frames with no matrix. The reader handles both.
  - If `moov` or the video track isn't found within 1 MiB, the Transcode is unreadable: a Link-only Post (not available) and a Problem Report.
- **Duration:** the asset's `duration`, an integer in milliseconds, rounded to whole seconds.
- **Oversized check:** the full `GET` response's `Content-Length` is read before any upload starts. If it is over **2,000,000,000 bytes**, the response is closed and the Video gets a Link-only Post (too large) plus a Problem Report. That byte limit is a safe reading of Telegram's "2000 MB". If `Content-Length` is missing, the upload goes ahead, and a Telegram rejection takes the upload-failure path.
- **Upload:** the Transcode body is streamed directly into the multipart `sendVideo` request, using chunked transfer. There are no temp files or shared volumes (ADR 0001), and the Bot API server accepts chunked bodies. Fields:
  - `chat_id`
  - `video`: a file part named `<id>.mp4` with type `video/mp4`
  - `thumbnail`: `attach://thumb`, plus a file part named `thumb`
  - `caption` and `parse_mode=HTML`
  - `supports_streaming=true`
  - `width` and `height`: the display dimensions from the header
  - `duration`
- **Thumbnail:**
  1. Fetched from `GET /api/assets/{id}/thumbnail?size=preview` (`asset.view`). This is JPEG by default and may be WebP depending on the Immich config, so it is decoded according to `Content-Type`.
  2. Scaled so the longest side is at most 320 px.
  3. Encoded as JPEG, lowering quality until it is under 204,800 bytes.

  If any step fails, the video goes out without a thumbnail and there is no Problem Report.

### Post content

- **Recording Date:** the asset's `localDateTime`. This is the wall-clock time at the place of recording, but it is serialised with a `Z` suffix. The service formats its UTC fields as-is, with **no time zone conversion**, as `02 Jan 2006, 15:04` (for example `26 Sep 2026, 19:04`).
- **Upload Date:** the asset's `createdAt`, a real instant, converted to the service's time zone (`TZ`, default UTC) and formatted the same way.
- **Caption** (HTML parse mode):
  ```
  📅 Recorded: <Recording Date>
  ⬆️ Uploaded: <Upload Date>
  ▶️ <a href="<IMMICH_PUBLIC_URL>/api/assets/<id>/original?key=<key>">Watch in original quality</a>
  🌐 <a href="<Share Link>">Open in Immich</a>
  ```
- **Link-only Post:** `sendMessage` with the same caption plus a short reason note. There are two variants:
  - "video too large for Telegram", for Oversized Videos
  - "video not available in Telegram", for timeout, unreadable Transcode or upload failures

  Link previews stay **enabled**, with `link_preview_options` `{"url": "<Share Link>"}`, so that Telegram shows the Share Link page's preview card rather than one for the original file, the first link.

### Outcomes per Video

| Situation | Video Channel | Log Channel |
|---|---|---|
| Ready, upload succeeds | Post with the Transcode | — |
| Transcode over 2,000,000,000 bytes | Link-only Post (too large) | Problem Report |
| Waiting longer than `WAIT_TIMEOUT` | Link-only Post (not available) | Problem Report |
| Transcode download fails or header unreadable | Link-only Post (not available) | Problem Report |
| Telegram upload fails (not 429) | Link-only Post (not available) | Problem Report |
| Share Link creation fails | nothing | Problem Report |
| Link-only Post fallback itself fails | nothing | Problem Report (noting both failures) |
| Trashed, archived, locked or deleted before posting | nothing | nothing |
| Telegram unreachable for the report too | nothing | container log only |

- **Problem Report contents:**
  - kind of problem
  - Recording Date
  - original filename (`originalFileName`)
  - a link to the asset in Immich: `IMMICH_PUBLIC_URL` + `/photos/` + id
  - the underlying error message

  Every Problem Report and every failure is also logged to stdout.
- **Telegram errors come in two forms, and both must be handled:**
  - JSON: `{"ok":false,"error_code":N,"description":"..."}`. A 429 adds `parameters.retry_after`.
  - A bare HTTP status with an empty body, for example 413, 400 or 501, from the Bot API server's HTTP layer.

### Lifecycle

- **Startup checks** run in order and fail fast with a non-zero exit and a clear message:
  1. The configuration is valid.
  2. Immich is reachable and its server version is ≥ 3.2.0.
  3. `GET /api/api-keys/me` succeeds. It needs no permission of its own. Its `permissions` list must contain `all`, or every one of the five required scopes. The error names each missing scope.
  4. `GET /api/users/me` resolves the Source User.
  5. `getMe` confirms the bot token is valid.
  6. For both the Video Channel and the Log Channel, `getChatMember` for the bot returns `status == "administrator"` with `can_post_messages == true`.
- **After the checks pass,** the service records the Watch Start and publishes `🟢 immich-tg started, watching uploads from <Watch Start>` to the Log Channel.
- **On SIGTERM or SIGINT,** it stops polling, finishes or abandons the current Video, publishes `🔴 immich-tg stopped` to the Log Channel and exits 0. Only failures caused by the shutdown's cancellation go unreported; any other failure still gets its Link-only Post and Problem Report, sent with a 5 s limit before the stopped message.

### Deployment

- Multi-stage Docker build producing a static binary on a minimal base image that runs as non-root and includes CA certificates.
- **`docker-compose.yml`:**
  - one service
  - `env_file: .env`
  - `restart: unless-stopped`
  - joins the external `telegram-bot-api` network
  - no published ports
  - no volumes
- `TELEGRAM_API_URL` is typically `http://telegram-bot-api:8081`. `IMMICH_URL` is typically Immich's LAN address.
- The README documents the prerequisites and setup order:
  1. Create the network.
  2. Start the Bot API server project.
  3. Create the bot and run `logOut` for it.
  4. Create both channels and add the bot as an admin in each.
  5. Configure Immich's transcoding settings.
  6. Create a scoped API key.
  7. Fill in `.env`.
  8. `docker compose up -d`.

## Testing Decisions

- **One seam, at the process edge.** Tests build the whole service from config, with an injected clock and poll trigger. It runs against two in-test HTTP fakes:
  - **Fake Immich** implements the endpoints used, backed by state the test can change: assets appear, get a Transcode, change visibility, get trashed, fail Share Link creation, fail or truncate downloads, belong to a partner, report a given version and key permissions. It also serves small MP4 fixtures with and without a rotation matrix, and one without a parseable `moov`.
  - **Fake Telegram Bot API** records every call, including chat ID, method, caption, parse mode, streaming flag, dimensions, duration, thumbnail and uploaded bytes. It can return JSON errors, a 429 with `retry_after`, or bare-status errors on demand.
- **Good tests assert only external behaviour:**
  - which messages reach which channel, in what order, with what content and file
  - the requests sent to Immich, such as the search filter and Share Link settings, where they are the contract
  - exit status and messages at startup

  No test reaches into internal structures. Refactoring the internals must not break any test.
- **Scenarios to cover:**
  - Watch Start excludes pre-existing Videos.
  - Partner Videos are ignored.
  - Motion-photo clips and archived, locked or trashed Videos are never posted. Waiting Videos that change like this are dropped.
  - Overlap deduplication: a Video seen in two polls gets exactly one Post.
  - Waiting until Ready, then a Post with the correct caption, dimensions and duration.
  - A portrait Transcode with a 90° matrix is sent with swapped (portrait) dimensions, and one without a matrix is sent as stored.
  - A burst is posted in upload order.
  - Oversized, timeout, download failure, unreadable header, upload failure, Share Link failure and fallback failure each produce the outcome in the table above.
  - A 429 during an upload is waited out and the upload repeated with a fresh stream, with no Problem Report.
  - A thumbnail failure still produces a normal Post.
  - The Recording Date is formatted without time zone conversion.
  - Started and stopped messages.
  - Each startup check fails with a clear error, including naming missing API key scopes.
  - The in-memory list stays bounded over many polls.
- **Prior art:** none. This is a greenfield repo, and these tests set the pattern. Standard library `httptest` servers are enough; no external mocking library is needed.

## Out of Scope

- **Persisted state of any kind:** checkpoints, catch-up after downtime, deduplication across restarts (ADR 0002).
- Backfill of Videos uploaded before the Watch Start.
- Syncing Immich changes to existing Posts, such as deletions, edits or Share Link removal (fire-and-forget).
- Retrying failed Posts, dead-letter queues, or manually re-posting from the Log Channel.
- Photos, albums, multiple Source Users and multiple Video Channels.
- Push-based detection (Immich workflows and webhooks, the websocket) and the Immich sync API, which rejects API keys.
- Transcoding inside immich-tg, and uploading originals.
- Posting as a Telegram user account (MTProto).
- The shared Telegram Bot API server project itself (its own repo).
- Checking Immich's transcoding settings, which would need admin permissions.
- Health endpoints, metrics, dashboards and localisation of captions.

## Further Notes

- **Manual acceptance check:** post a portrait Pixel video that Immich transcoded with Quick Sync, and confirm it plays upright in the Telegram app on a phone. This is the one behaviour no source check can guarantee: that Telegram's players honour the Transcode's rotation matrix.
- **Facts verified from source:**
  - Immich main at `f8f4051`, v3.2.x: search DTOs and filter builder, `media.ts` transcoding and rotation, the shared-link DTO, the web routes, and the `api-keys/me` endpoint.
  - `tdlib/telegram-bot-api` with td `bc9c263`: `HttpReader` chunked support and file-size caps.
- **For the Bot API server project's README, not this repo:** the server writes each uploaded file completely to its temp directory (`$TMPDIR`, or `/tmp` inside the container) before sending it to Telegram. That location needs about 2 GB free per upload in progress, or point the server's temp dir at the data volume.
- A crash in the middle of a Post can lose it or, rarely, duplicate it. This is accepted (ADR 0002).
- Domain vocabulary follows CONTEXT.md. Decisions are recorded in docs/adr/0001–0003.
