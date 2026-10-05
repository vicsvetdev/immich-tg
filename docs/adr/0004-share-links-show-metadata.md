# Share Links show metadata, so the original can be played

A Post links straight to the Video's original file, `<IMMICH_PUBLIC_URL>/api/assets/<id>/original?key=<Share Link key>`, so that viewers can play it in the browser. The Share Link page itself plays only the Transcode. The original opens only through a Share Link that allows downloading, and Immich creates a link that hides metadata with downloading off, whatever the request asks (`allowDownload: dto.showMetadata === false ? false : …` in its `SharedLinkService.create`, checked in Immich 3.2.4). Share Links are therefore created with `showMetadata: true`.

Hiding metadata would protect little anyway: the original MP4 itself carries the recording location (Pixel writes it in the `location` tag), so anyone who can open the original can read it.

Creating the link with metadata hidden and then allowing downloads with a `PATCH` was rejected. Immich's update doesn't apply the rule today, but relying on that inconsistency would break silently once Immich fixes it, and it needs the extra `sharedLink.update` permission.

## Consequences

- Anyone with a Post's links can see where and with what the Video was recorded, both on the Share Link page and in the original file.
- immich-tg checks the `allowDownload` Immich returns. A Share Link without downloading is a failed Share Link creation: a Problem Report and no Post, rather than a Post whose link is dead.
- Whether the original plays in the browser depends on the browser: Pixel records HEVC, which Safari plays, and Chrome and Edge play with hardware decoding. Posts also link the Share Link page, which plays the Transcode anywhere.
