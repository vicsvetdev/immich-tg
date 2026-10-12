# Depend on Immich transcoding every video to 1080p H.264

immich-tg only ever uploads Immich's Transcode, never the original, and treats a Video as Ready once that Transcode exists. This relies on an Immich admin setting outside this repo: Video Transcoding with policy "All videos", target resolution 1080p, video codec H.264, a maximum bitrate such as 12M, and hardware decoding off. With policy "All", every Video gets a Transcode, so readiness is deterministic. Under the "Optimal" or "Required" policies, "still transcoding" and "will never be transcoded" look the same from outside. Transcoding with ffmpeg inside immich-tg was rejected as too heavy.

## Consequences

- Changing the Immich transcoding policy away from "All" means Videos never become Ready, and each one ends in a Link-only Post after `WAIT_TIMEOUT`.
- Every Video costs Immich CPU time and extra disk space for the Transcode.
- Without a maximum bitrate, H.264 re-encoding of 60 fps HEVC originals can run at 50 Mbit/s, making a 6-minute Video an Oversized Video.
- Hardware decoding must be off. With it on, Immich decodes with `-noautorotate`, so portrait Videos keep landscape frames plus a rotation matrix in the MP4 header. Telegram for Android and desktop honour the matrix, but Telegram for iPhone ignores it and stretches the stored frames to the Post's dimensions, so the Video plays sideways. With hardware decoding off, ffmpeg rotates the frames, at the cost of decoding on the CPU, and the Transcode has no matrix. Rotating the frames in immich-tg was rejected for the same reason as transcoding there.
- Hardware encoding, e.g. Quick Sync, is allowed. immich-tg still reads the display dimensions from each Transcode's MP4 header instead of predicting them from Immich's settings. A Transcode whose header rotates its frames is still posted, since it plays upright outside the iPhone app, but it also gets a Problem Report, so that hardware decoding left on is noticed.
