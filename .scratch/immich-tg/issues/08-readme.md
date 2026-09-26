# 08 — README: prerequisites and setup order

**What to build:** Someone with a running Immich can deploy immich-tg from scratch by following the README alone.

**Blocked by:** 03 — Posts carry the Transcode as a playable video; 07 — Startup checks and graceful shutdown.

**Status:** ready-for-agent

- [ ] **Overview:** what the service does, in the domain terms from CONTEXT.md, and what it deliberately doesn't do:
  - no catch-up after downtime
  - fire-and-forget Posts
  - no retries
- [ ] **Immich prerequisites:**
  - version ≥ 3.2.0
  - Video Transcoding policy "All videos", 1080p, H.264, AAC
  - hardware acceleration is fine
  - what happens if the settings are wrong (timeouts, then Link-only Posts)
- [ ] **API key:** how to create a scoped key with exactly `user.read`, `asset.read`, `asset.view`, `asset.share` and `sharedLink.create`.
- [ ] **Telegram:**
  - create the bot with BotFather
  - run `logOut` once against the cloud Bot API
  - create the Video Channel and the Log Channel, and add the bot as an admin with "post messages" in each
  - how to find the channel IDs
- [ ] **Bot API server:** it is a separate project. Create the `telegram-bot-api` network once, and start that project first.
- [ ] **Configure and run:** fill in `.env` from `.env.example`, run `docker compose up -d`, and check `docker logs` and the started message.
- [ ] **Troubleshooting:** each startup-check failure message, and how to read Problem Reports.
- [ ] **Manual acceptance check:** a portrait video plays upright in the Telegram app.
