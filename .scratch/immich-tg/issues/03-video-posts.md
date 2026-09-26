# 03 — Posts carry the Transcode as a playable video

**What to build:** Posts in the Video Channel become inline, streamable Telegram videos (the Transcode) with the same caption as before. Portrait Pixel videos play upright, whether Immich transcoded them with Quick Sync (landscape frames plus a rotation matrix) or with the software fallback (frames already rotated).

The original file is never uploaded. See the spec: "Transcode handling". See also ADR 0001 (streamed HTTP upload, no shared volumes) and ADR 0003 (dimensions read from the Transcode header).

**Blocked by:** 02 — Detect New Videos and post them as text.

**Status:** ready-for-agent

- [ ] **Header read:** a ranged `GET /api/assets/{id}/video/playback` for the first 1 MiB is parsed by a minimal MP4 reader:
  - it finds the video track (`hdlr` = `vide`)
  - it reads the `tkhd` width and height (16.16 fixed point) and the rotation from the `tkhd` matrix
- [ ] **Display dimensions:** equal to the stored dimensions, swapped when the rotation is 90° or 270°.
- [ ] **Duration:** the asset's `duration` in ms, rounded to whole seconds.
- [ ] **Upload:** the Transcode from a full `GET` playback is streamed directly into a multipart `sendVideo` request using chunked transfer, with no temp files. Fields:
  - `chat_id`
  - `video` (`<id>.mp4`, type `video/mp4`)
  - `caption` and `parse_mode=HTML`
  - `supports_streaming=true`
  - `width`, `height` and `duration`
- [ ] **Replaces the text Post:** the video Post takes the place of the text Post from 02 for the normal Ready path.
- [ ] **Fixtures:** the fake Immich serves small MP4 fixtures, one with a 90° matrix and one physically portrait without a matrix.
- [ ] **Rotation tests:** the 90° fixture is sent with swapped (portrait) dimensions, and the unrotated fixture as stored.
- [ ] **Upload bytes:** a test asserts that the bytes received by the fake Telegram equal the Transcode.
- [ ] **Manual acceptance:** a real portrait Pixel video transcoded by Immich with Quick Sync plays upright in the Telegram app on a phone.
