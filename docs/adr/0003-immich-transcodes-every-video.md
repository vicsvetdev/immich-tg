# Depend on Immich transcoding every video to 1080p H.264

immich-tg only ever uploads Immich's Transcode, never the original, and treats a Video as Ready once that Transcode exists. This relies on an Immich admin setting outside this repo: Video Transcoding with policy "All videos", target resolution 1080p, video codec H.264. With policy "All", every Video gets a Transcode, so readiness is deterministic. Under the "Optimal" or "Required" policies, "still transcoding" and "will never be transcoded" look the same from outside. Transcoding with ffmpeg inside immich-tg was rejected as too heavy.

## Consequences

- Changing the Immich transcoding policy away from "All" means Videos never become Ready, and each one ends in a Link-only Post after `WAIT_TIMEOUT`.
- Every Video costs Immich CPU time and extra disk space for the Transcode.
- Any hardware acceleration setting is allowed. With Quick Sync plus hardware decoding, Immich keeps portrait Videos as landscape frames plus a rotation matrix, while its software fallback rotates the frames. immich-tg therefore reads the display dimensions from each Transcode's MP4 header instead of predicting them from Immich's settings.
