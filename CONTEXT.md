# immich-tg

Forwards videos newly uploaded to a self-hosted Immich instance into a Telegram channel.

## Language

**Source User**:
The single Immich user whose uploads are watched. Only their videos are ever forwarded.
_Avoid_: owner, account

**Video**:
An Immich asset of type video that the Source User uploaded themselves. The embedded clip of a motion photo (e.g. Pixel "Top Shot"/motion) is not a Video.
_Avoid_: clip, media, file

**New Video**:
A Video uploaded to Immich after the current Watch Start. Recording date is irrelevant. Videos uploaded before the Watch Start, including those uploaded while the service was down, are never posted.
_Avoid_: recent video, latest video

**Transcode**:
The 1080p H.264 MP4 copy that Immich produces for every Video. It is the only file ever uploaded to Telegram; the original never is.
_Avoid_: encoded video, preview, converted file

**Ready**:
A New Video is Ready once Immich has produced its Transcode. Only Ready Videos are posted; until then the Video is Waiting.
_Avoid_: processed, done

**Video Channel**:
The Telegram channel where Posts are published; what the audience follows.
_Avoid_: channel, main channel, target

**Log Channel**:
A separate Telegram channel, for the operator only, where Problem Reports are published.
_Avoid_: error channel, admin channel

**Problem Report**:
A message in the Log Channel describing why a New Video could not be handled as intended (e.g. no Transcode in time, upload failed). Never retried; one report per problem.
_Avoid_: alert, error, notification

**Post**:
The single Video Channel message published for one New Video. Carries the Recording Date, the Upload Date and the Share Link, and the Transcode itself unless the Post is Link-only. Once published, a Post is never edited or deleted by the service, even if the Video later changes or disappears in Immich.
_Avoid_: message, notification

**Link-only Post**:
A Post without the video file: Recording Date, Upload Date, Share Link and a short note why. Used for Oversized Videos and whenever the Transcode could not be obtained or uploaded.

**Recording Date**:
When the Video was captured, as wall-clock time at the place of recording. Not the upload time.
_Avoid_: upload date, created date, timestamp

**Upload Date**:
When the Video was uploaded to Immich (its `createdAt`), shown in the service's time zone (`TZ`). Shown in a Post next to the Recording Date, each labelled, so the two are never confused.
_Avoid_: created date, timestamp

**Oversized Video**:
A Video whose Transcode is too large for the bot to upload to Telegram (over 2 GB). At 1080p this means roughly a recording of 30+ minutes.
_Avoid_: big video, large file

**Share Link**:
A public Immich link that lets anyone with the URL view that one Video in Immich, in original quality, without an Immich account.
_Avoid_: public link, Immich link, URL

**Watch Start**:
The moment the currently running service instance started. Resets on every restart; nothing is remembered between runs.
