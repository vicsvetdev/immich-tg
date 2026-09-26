# immich-tg

immich-tg posts the videos you upload to your self-hosted [Immich](https://immich.app) into a Telegram channel.

It is a small, stateless Go service that runs in Docker. It watches the uploads of one Immich user, the **Source User** (the owner of the API key). When that user uploads a **New Video**, immich-tg waits until the Video is **Ready**, meaning Immich has made its **Transcode**: a 1080p H.264 MP4 copy. It then publishes one **Post** to the **Video Channel**. A Post contains:

- the Transcode as an inline, streamable Telegram video, with the right aspect ratio, duration and a preview thumbnail. Portrait videos play upright.
- the **Recording Date**: the local time where the video was recorded, not when it was uploaded.
- a **Share Link**, "Watch in original quality". It opens the Video in Immich in original quality. Viewers need no account and no password, and the link doesn't expire. Metadata such as location is hidden, and downloading is allowed.

The original file is never uploaded to Telegram, only the Transcode.

Sometimes a Video can't be posted as a video. This happens when the Video is an **Oversized Video**, meaning its Transcode is over 2 GB (2,000,000,000 bytes). It also happens when the Transcode never appears, or can't be read or uploaded. The Video then gets a **Link-only Post** instead: the Recording Date, the Share Link and a short note explaining why there is no player. Each such problem also produces a **Problem Report** in a private **Log Channel**, which only you, the operator, follow. The Log Channel also receives a message whenever immich-tg starts or stops.

Uploads go through a Telegram bot and a self-hosted [Telegram Bot API server](https://github.com/tdlib/telegram-bot-api) running in `--local` mode. The public Bot API accepts uploads only up to 50 MB; the local server raises that to 2000 MB. The Bot API server is a separate project, shared with other bots (see [ADR 0001](docs/adr/0001-bot-via-shared-local-bot-api-server.md)).

## What it deliberately doesn't do

- **No catch-up after downtime.** Only Videos uploaded after the current **Watch Start**, the moment the running instance started, are posted. Videos uploaded while immich-tg was down, and everything uploaded before its first start, are never posted. Nothing is stored between runs. The started and stopped messages in the Log Channel are the only record of gaps ([ADR 0002](docs/adr/0002-stateless-service.md)).
- **Fire-and-forget Posts.** A Post is never edited or deleted once it is published. This holds even if you later edit, trash or delete the Video in Immich, or remove its Share Link.
- **No retries.** Each New Video is handled once. If something fails, you get a Link-only Post and/or a Problem Report, and that's it. The only thing that is repeated is a call that Telegram rate-limits (HTTP 429). immich-tg waits the time Telegram asks for and makes the call again, with no Problem Report.

Other limits:

- It handles videos only: no photos, albums, multiple Source Users or multiple Video Channels.
- A crash in the middle of a Post can lose that Post or, rarely, post it twice.

What counts as a Video:

- A Video uploaded after the Watch Start is a New Video, however old the recording is: "new" means newly uploaded.
- Motion-photo clips (such as Pixel Top Shots) are never posted, and neither are partners' videos.
- If you trash, archive, lock or delete a Video before it is posted, it is dropped silently.

The domain vocabulary is defined in [CONTEXT.md](CONTEXT.md), and design decisions are recorded in [docs/adr](docs/adr).

## Requirements

- **Immich v3.2.0 or newer.** immich-tg uses the search filter format introduced in 3.2, and it refuses to start on anything older.
- **Admin access to Immich,** to change its Video Transcoding settings (see [step 5](#5-configure-immichs-video-transcoding)).
- **A Docker host** with Docker Compose. By default the Bot API server runs on the same host, because they talk over a shared Docker network and no ports are published.
- **Network access.** The immich-tg container must be able to reach Immich at its LAN address.
- **Clocks that roughly agree.** immich-tg compares upload times from Immich's clock with its own clock. Running on the same host as Immich, or using NTP, is enough.
- **A Telegram account,** to create the bot and the two channels.

## Setup

Follow these steps in this order.

### 1. Create the shared Docker network

immich-tg and the Bot API server talk over a pre-created external network called `telegram-bot-api`. Create it once:

```sh
docker network create telegram-bot-api
```

### 2. Start the Bot API server

The Telegram Bot API server is **a separate compose project**. It is not part of this repo, and other bots can share it. Set it up by following its own instructions, then start it **before** immich-tg. immich-tg expects the server to:

- run in `--local` mode, so that uploads up to 2000 MB are accepted;
- be attached to the `telegram-bot-api` network;
- be reachable there as `telegram-bot-api` on port `8081`, which is the `TELEGRAM_API_URL=http://telegram-bot-api:8081` given in `.env.example`.

If your server runs on another host instead, publish its port there and point `TELEGRAM_API_URL` at it; no code changes are needed.

### 3. Create the bot and log it out of the cloud Bot API

1. In Telegram, open [@BotFather](https://t.me/BotFather), send `/newbot` and follow the prompts. BotFather replies with the bot token, which looks like `123456789:AA…`. That token is `TELEGRAM_BOT_TOKEN`.
2. Log the bot out of Telegram's cloud Bot API **once**. A bot that is still logged in to the cloud can't be used reliably through a local server:

   ```sh
   curl -s "https://api.telegram.org/bot<TOKEN>/logOut"
   # {"ok":true,"result":true}
   ```

   After this, the bot works only through your local Bot API server, which is expected. Telegram doesn't let a logged-out bot log back in to the cloud API for 10 minutes.

### 4. Create the Video Channel and the Log Channel

1. Create two Telegram channels:
   - the **Video Channel**, which your audience follows;
   - the **Log Channel**, a private channel for you only.
2. In **each** channel, add the bot as an **administrator** with the **Post messages** right. Go to *Channel info → Administrators → Add Admin*, search for the bot's username, and keep "Post messages" enabled. No other rights are needed.
3. Find each channel's ID, which goes into `TELEGRAM_VIDEO_CHANNEL_ID` and `TELEGRAM_LOG_CHANNEL_ID`:
   - **Public channel:** you can use its username, like `@mychannel`.
   - **Private channel:** you need the numeric ID, such as `-1001234567890`. The easiest way to get it is to ask your local Bot API server. With the bot already an admin, post any message in the channel, then run:

     ```sh
     docker run --rm --network telegram-bot-api curlimages/curl -s \
       "http://telegram-bot-api:8081/bot<TOKEN>/getUpdates"
     ```

     Look for `"channel_post"`. Its `"chat":{"id":-100…,"title":"…"}` is the channel's ID. Only updates from the last 24 hours are returned, so post the message just before you run the command.

     Another way is to open the channel in [Telegram Web](https://web.telegram.org). The number at the end of the URL is the channel's ID. If it doesn't start with `-100`, add `-100` in front of the digits.

### 5. Configure Immich's Video Transcoding

immich-tg only ever uploads Immich's Transcode, so Immich has to make one for **every** Video ([ADR 0003](docs/adr/0003-immich-transcodes-every-video.md)). As an Immich admin, go to *Administration → Settings → Video Transcoding Settings* and set:

| Setting | Value |
|---|---|
| Transcode policy | **All videos** |
| Target resolution | **1080p** |
| Video codec | **H.264** |
| Audio codec | **AAC** (the default) |

Any **hardware acceleration** setting works, including Quick Sync with hardware decoding. immich-tg reads each Transcode's orientation from the file itself, so portrait videos come out upright either way.

immich-tg can't check these settings, because that would need admin permissions. **If they are wrong, nothing fails at startup.** Instead:

- If the policy isn't "All videos", Immich skips transcoding some Videos, and those Videos never become Ready. After `WAIT_TIMEOUT` (default 2 hours), each one gets a Link-only Post saying "video not available in Telegram", plus a "no Transcode in time" Problem Report.
- With another codec or resolution, the Transcode may be too large or may not play inline in Telegram.

Only New Videos matter, so you don't need to transcode your existing library. If you do start a transcoding job for the whole library, New Videos wait in the queue behind it and may hit `WAIT_TIMEOUT`.

### 6. Create a scoped API key

Create the key as the **Source User**, the Immich user whose uploads should be posted:

1. In Immich, click your avatar and open *Account Settings → API Keys → New API Key*.
2. Give the key a name, such as `immich-tg`, and grant **exactly** these permissions:

   | Permission | Used for |
   |---|---|
   | `user.read` | finding out who the Source User is |
   | `asset.read` | searching for New Videos |
   | `asset.view` | downloading the Transcode and the preview thumbnail |
   | `asset.share` | including the Video in a Share Link |
   | `sharedLink.create` | creating the Share Link |

3. Copy the key; it goes into `IMMICH_API_KEY`.

immich-tg never downloads originals, so `asset.download` isn't needed. A key with all permissions also works, but a scoped key does less harm if it leaks. At startup, immich-tg checks the key and names any permission that is missing.

### 7. Fill in `.env`

```sh
git clone <this repo> immich-tg && cd immich-tg
cp .env.example .env
```

Edit `.env`. [`.env.example`](.env.example) documents every variable:

| Variable | Required | Description |
|---|---|---|
| `IMMICH_URL` | yes | Immich URL used for API calls and for downloading the Transcode. Typically its LAN address, e.g. `http://192.168.1.10:2283`. It must be reachable from inside the container: `localhost` and Immich's own container name won't work. |
| `IMMICH_PUBLIC_URL` | yes | Public Immich URL, e.g. `https://photos.example.com`. Share Links are `<IMMICH_PUBLIC_URL>/share/<key>`, and Problem Reports link to `<IMMICH_PUBLIC_URL>/photos/<id>`. Viewers must be able to open it. |
| `IMMICH_API_KEY` | yes | The scoped key from step 6. |
| `TELEGRAM_API_URL` | yes | The Bot API server, typically `http://telegram-bot-api:8081`. |
| `TELEGRAM_BOT_TOKEN` | yes | The bot token from step 3. |
| `TELEGRAM_VIDEO_CHANNEL_ID` | yes | A numeric ID such as `-1001234567890`, or `@channelname`. |
| `TELEGRAM_LOG_CHANNEL_ID` | yes | Same format as the Video Channel's ID. |
| `POLL_INTERVAL` | no | How often immich-tg asks Immich for New Videos, as a Go duration. Default `30s`. |
| `WAIT_TIMEOUT` | no | How long a New Video may wait for its Transcode before it gets a Link-only Post and a Problem Report. Default `2h`. Set it above the longest time Immich takes to transcode, including time spent in the queue. |
| `TZ` | no | Time zone for the Watch Start in the started message, e.g. `Europe/Stockholm`. Default UTC. Recording Dates never depend on it. |

### 8. Start immich-tg

```sh
docker compose up -d --build
```

This builds the image from this repo and starts one container. The container uses `.env`, restarts automatically (`unless-stopped`), joins the `telegram-bot-api` network and publishes no ports.

Check the logs, which go to stdout as JSON lines:

```sh
docker compose logs -f immich-tg
# or: docker logs -f immich-tg-immich-tg-1
```

A good start looks like this:

```json
{"time":"…","level":"INFO","msg":"started","watch_start":"…","source_user_id":"…","poll_interval":"30s","wait_timeout":"2h0m0s"}
```

The Log Channel also shows:

```
🟢 immich-tg started, watching uploads from 26 Sep 2026, 19:04:05 CEST
```

Now upload a video to Immich. Within about `POLL_INTERVAL` it is logged as `New Video waiting`. Once Immich has made its Transcode, it is logged as `posted`, and its Post appears in the Video Channel.

## Operating

- **Stop:** `docker compose down`, or `docker compose stop`, sends SIGTERM. immich-tg stops polling and either finishes or abandons the Video in progress. An abandoned Video gets no Problem Report; it is logged as `Video abandoned on shutdown`. immich-tg then publishes `🔴 immich-tg stopped` to the Log Channel and exits 0.
- **Crash:** if immich-tg crashes, Docker restarts it and you get a new started message **without** a stopped message before it. That's how you tell crashes from planned stops. Remember that Videos uploaded between the stop and the next start are never posted.
- **Update:** `git pull && docker compose up -d --build`.
- **Change configuration:** edit `.env`, then run `docker compose up -d` to recreate the container.

## Troubleshooting

### Startup check failures

Before it posts the started message, immich-tg checks its configuration and its access to Immich and Telegram, in the order below. The first failure is logged, and the process exits with status 1. Because of `restart: unless-stopped`, Docker keeps restarting it, so the same error repeats in the logs. Nothing reaches the Log Channel until every check passes. After fixing `.env`, run `docker compose up -d` to recreate the container. After fixing Immich or the channels, the next automatic restart picks up the fix, or run `docker compose restart immich-tg` to retry at once.

**The configuration is invalid.** The log line is `"msg":"invalid configuration"`, and its `error` field lists every bad variable at once:

| Message | Fix |
|---|---|
| `<VAR> is required` | Set the variable in `.env`. |
| `<VAR> must be an absolute http or https URL, got "…"` | Use a full URL, e.g. `http://192.168.1.10:2283`. |
| `<VAR> must be a numeric chat ID such as -1001234567890 or a @channelname, got "…"` | See [step 4](#4-create-the-video-channel-and-the-log-channel). |
| `POLL_INTERVAL must be a positive duration such as 30s, got "…"` <br> `WAIT_TIMEOUT must be a positive duration such as 2h0m0s, got "…"` | Use a Go duration with a unit, e.g. `45s`, `5m` or `3h`. |

**A startup check fails.** The log line is `"msg":"startup check failed"`, with a `check` field and an `error` field:

| `check` | Message | Cause and fix |
|---|---|---|
| `immich_version` | `Immich is not reachable at IMMICH_URL <url>: …` | Immich is down, or `IMMICH_URL` can't be reached from the container: use Immich's LAN address, not `localhost` or its container name. If the error ends in `decode response: …`, something answered but it isn't Immich's API: check that `IMMICH_URL` points at Immich's server port rather than another service or path. |
| `immich_version` | `could not read the server version of Immich at IMMICH_URL <url>: … HTTP <status> …` | Something answered with an error status. Check that `IMMICH_URL` points at Immich itself, with no extra path, and not at a proxy that blocks it. |
| `immich_version` | `Immich v3.1.0 is not supported, immich-tg needs v3.2.0 or newer` | Upgrade Immich. |
| `api_key_permissions` | `could not read the permissions of IMMICH_API_KEY: … HTTP 401 …` | The key is wrong, or it was deleted. Create a new one ([step 6](#6-create-a-scoped-api-key)). |
| `api_key_permissions` | `IMMICH_API_KEY is missing the permissions asset.share, sharedLink.create` | Give the key the listed permissions, or create a new key with all five. |
| `source_user` | `could not resolve the Source User, the user IMMICH_API_KEY belongs to: …` | Immich couldn't return the key's user. Check the error text and Immich's logs. |
| `bot_token` | `could not identify the bot, check TELEGRAM_BOT_TOKEN: telegram getMe: 401 Unauthorized` | The token is wrong. Copy it again from @BotFather. |
| `bot_token` | `could not identify the bot, check TELEGRAM_BOT_TOKEN: telegram getMe: Post "http://telegram-bot-api:8081/bot<token>/getMe": dial tcp: lookup telegram-bot-api … no such host` (or `connection refused`) | This is the first Telegram call, so the Bot API server is the problem, not the token. The server isn't running, isn't on the `telegram-bot-api` network, or `TELEGRAM_API_URL` is wrong ([steps 1–2](#1-create-the-shared-docker-network)). |
| `channel_admin` | `could not check the bot in the Video Channel, TELEGRAM_VIDEO_CHANNEL_ID <id>: telegram getChatMember: 400 Bad Request: chat not found` | The ID is wrong, or the bot isn't in that channel. For private channels, check the `-100` prefix. |
| `channel_admin` | `the bot cannot post to the Log Channel, TELEGRAM_LOG_CHANNEL_ID <id>: it must be an administrator allowed to post messages, but its status is administrator and can_post_messages is false` | Give the bot the "Post messages" right in that channel. If its status is `left` or `kicked`, add it back as an admin ([step 4](#4-create-the-video-channel-and-the-log-channel)). |

After the checks pass, `could not publish the started message to the Log Channel` means Telegram rejected the message itself, or the Bot API server stopped answering. The `error` field says which.

### Reading Problem Reports

A Problem Report is one message in the Log Channel about one New Video that couldn't be handled as intended. It is never retried, and there is only one per problem. It looks like this:

```
⚠️ Problem: no Transcode in time
📅 26 Sep 2026, 19:04
📄 PXL_20260926_170412345.mp4
🔗 https://photos.example.com/photos/<asset id>
Reason: Immich produced no Transcode within WAIT_TIMEOUT (2h0m0s)
```

The lines are:

1. the kind of problem
2. the Recording Date
3. the original file name
4. a link to the Video in Immich, for you; it is not a Share Link
5. the underlying error

The same information is logged to stdout as `"msg":"Problem Report"`. To list all Problem Reports:

```sh
docker compose logs immich-tg | grep '"msg":"Problem Report"'
```

The kind on the first line is one of `oversized`, `no Transcode in time`, `Transcode download failed`, `Transcode header unreadable`, `upload failed` or `Share Link creation failed`.

An upload has no overall time limit, because 2 GB can take a while. Instead, it is abandoned once no data has moved for 2 minutes, and the Reason line says which side stalled: Immich (`Transcode download failed`) or the Bot API server (`upload failed`). Once the whole file is sent, immich-tg waits up to 1 hour for the Bot API server to answer, which leaves it time to pass a large file on to Telegram.

What each situation produces, and what to check:

| Situation | Video Channel | Log Channel | What to check |
|---|---|---|---|
| Ready, upload succeeds | Post with the video | — | — |
| Transcode over 2,000,000,000 bytes (Oversized Video) | Link-only Post: "video too large for Telegram" | Problem Report (`oversized`) | Nothing to fix. At 1080p this is roughly a recording of 30 minutes or more. Viewers can still watch it through the Share Link. |
| Waiting longer than `WAIT_TIMEOUT` | Link-only Post: "video not available in Telegram" | Problem Report (`no Transcode in time`) | Immich's transcoding settings ([step 5](#5-configure-immichs-video-transcoding)), Immich's job queue and logs, and whether `WAIT_TIMEOUT` is long enough. |
| Transcode download fails (an error, a download that breaks off, or Immich sending nothing for 2 minutes) | Link-only Post: "video not available in Telegram" | Problem Report (`Transcode download failed`) | Immich's reachability and logs. |
| The Transcode's header can't be read | Link-only Post: "video not available in Telegram" | Problem Report (`Transcode header unreadable`) | No `moov` box or video track was found in the first 1 MiB of what Immich served. Check Immich's transcoding settings and logs. |
| Telegram upload fails (the upload is rejected or breaks off, the Bot API server takes no data for 2 minutes, or it doesn't answer within 1 hour of receiving the whole file) | Link-only Post: "video not available in Telegram" | Problem Report (`upload failed`) | The Bot API server's logs and free disk space. Also check that it runs in `--local` mode, because otherwise anything over 50 MB is rejected. |
| Share Link creation fails | nothing | Problem Report (`Share Link creation failed`) | The API key's `asset.share` and `sharedLink.create` permissions, and Immich's logs. |
| The Link-only Post itself fails too | nothing | one Problem Report, of the original kind, noting both failures | Both errors are in the Reason line. |
| Trashed, archived, locked or deleted before posting | nothing | nothing | This is intended: what you do in Immich takes precedence. |
| Telegram unreachable, even for the Problem Report | nothing | nothing | The Problem Report is only in the container logs, as `"msg":"Problem Report"` followed by `could not publish the Problem Report to the Log Channel`. |

Some things are not reported, by design:

- **Rate limiting.** When Telegram rate-limits a call (429), immich-tg waits the time Telegram asks for and repeats the call; an upload then restarts with a fresh download from Immich. During a burst of uploads, Posts may arrive more slowly, but no Problem Report is made.
- **Missing thumbnail.** The thumbnail is made from Immich's preview image. If that fails, the Post goes out without a thumbnail, and nothing is sent to the Log Channel.

### Nothing is posted, and there are no Problem Reports

- **The video was uploaded while immich-tg was down,** or before it started. Such videos are never posted. Compare the upload time with the started and stopped messages in the Log Channel.
- **The video isn't the Source User's own upload.** It might be a partner's, or it might have been uploaded with a different user's key.
- **It's a motion-photo clip,** or it was archived, locked or trashed.
- **It's still Waiting.** The logs show `New Video waiting` but no `posted` yet, because Immich hasn't made the Transcode. Once `WAIT_TIMEOUT` passes, the Video gets a Link-only Post and a Problem Report.
- **The search fails.** The logs show `could not search Immich for New Videos`. immich-tg tries again on the next poll and doesn't miss anything meanwhile, but it can't find New Videos until Immich answers.
- **The clocks are far apart.** If the Immich host's clock is behind immich-tg's, uploads made just after the Watch Start look older than it and are ignored. Enable NTP.

## Manual acceptance check

The automated tests can't check this one: whether Telegram's players honour the Transcode's rotation. Check it once after setup, and again after changing Immich's hardware acceleration:

1. With Immich's hardware acceleration set to **Quick Sync** with hardware decoding, record a short **portrait** video on a Pixel phone and let it back up to Immich. Quick Sync stores portrait video as landscape frames plus a rotation, which is the case to check. If you use a different acceleration setting, test with that one too.
2. Wait for its Post in the Video Channel.
3. Open the Post in the **Telegram app on a phone**. Check that:
   - the video plays **upright**, in a portrait frame, and streams without first downloading completely;
   - its thumbnail is upright;
   - the Recording Date is right;
   - "Watch in original quality" opens the Video in Immich.

If the video plays sideways, find its `"msg":"posted"` log line. It shows the `width`, `height` and `rotation` that immich-tg read from the Transcode.

## Development

```sh
go test ./...
```

The tests drive the whole service against a fake Immich and a fake Telegram Bot API, with no real time passing. See `internal/fakeimmich` and `internal/faketelegram`.
