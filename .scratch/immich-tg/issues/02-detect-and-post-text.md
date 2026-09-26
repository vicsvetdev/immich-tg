# 02 — Detect New Videos and post them as text

**What to build:** The operator uploads a video from their phone. Shortly after Immich produces its Transcode, a Post appears in the Video Channel with the Recording Date and a "Watch in original quality" Share Link.

In this ticket the Post is text only; 03 upgrades it to a video. Videos from before the Watch Start, partners' Videos and motion-photo clips never appear.

See the spec: "Detection (Immich)", "Share Link" and "Post content".

**Blocked by:** 01 — Walking skeleton.

**Status:** ready-for-agent

- [ ] **Source User:** resolved via `GET /api/users/me`, with the `x-api-key` header.
- [ ] **Two searches per poll:** each poll issues the candidates search and the Ready search (`isEncoded: {eq: true}`) using the new filter format:
  - type VIDEO
  - visibility timeline
  - `trashedAt: {eq: null}`
  - `createdAt >= window start`
  - `size` 1000
  - pages followed via `nextCursor`
- [ ] **Client-side filtering and sorting:** only items whose `ownerId` equals the Source User are kept, sorted by `createdAt` ascending.
- [ ] **Search window:** start = max(Watch Start, min(newest seen `createdAt` − 5 min, oldest Waiting `createdAt`)), and it never moves backwards.
- [ ] **Waiting:** a new candidate with `createdAt` ≥ Watch Start becomes Waiting, and its first-seen time is recorded.
- [ ] **Ready:** a Waiting Video that appears in the Ready results is published.
- [ ] **Order:** Videos are processed one at a time, oldest upload first.
- [ ] **Share Link:** created with `{"type":"INDIVIDUAL","assetIds":[id],"allowDownload":true,"showMetadata":false,"allowUpload":false}`. The URL is `IMMICH_PUBLIC_URL` + `/share/` + `key`.
- [ ] **Text Post:** sent to the Video Channel with parse mode HTML:
  ```
  📅 <Recording Date>
  ▶️ <a href="<Share Link>">Watch in original quality</a>
  ```
  The Recording Date is `localDateTime`, formatted from its UTC fields with no conversion as `02 Jan 2006, 15:04`.
- [ ] **No duplicates:** a Video returned by several overlapping polls is posted exactly once.
- [ ] **Bounded memory:** handled entries older than the window start are forgotten, and a test shows the list stays bounded over many polls.
- [ ] **Polling:** runs every `POLL_INTERVAL`.
- [ ] **Tests** cover:
  - pre-Watch-Start Videos are excluded
  - partner Videos are ignored
  - the search request bodies (as the contract)
  - Recording Date formatting
  - upload order
  - deduplication
