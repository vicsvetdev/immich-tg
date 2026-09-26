# 01 — Walking skeleton: the service starts and announces itself

**What to build:** The operator fills in `.env`, runs `docker compose up -d`, and sees `🟢 immich-tg started, watching uploads from <Watch Start>` in the Log Channel.

This ticket lays the foundation every later ticket builds on:
- the Go module
- config loading
- the in-memory Watch Start
- a minimal Telegram client (`sendMessage` only)
- the Docker packaging
- the test setup at the single seam: the whole service driven against a fake Immich and a fake Telegram Bot API, with an injectable clock and a way to trigger a poll

See the spec (`.scratch/immich-tg/specs.md`): Modules, Deployment and Testing Decisions. See also ADR 0001 (shared Bot API server reached over the external network) and ADR 0002 (stateless).

**Blocked by:** None — can start immediately.

**Status:** ready-for-agent

- [ ] **Required variables load and are validated:** `IMMICH_URL`, `IMMICH_PUBLIC_URL`, `IMMICH_API_KEY`, `TELEGRAM_API_URL`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_VIDEO_CHANNEL_ID`, `TELEGRAM_LOG_CHANNEL_ID`.
- [ ] **Optional variables load with defaults:** `POLL_INTERVAL` (30s) and `WAIT_TIMEOUT` (2h).
- [ ] **Bad config fails fast:** a missing or invalid variable exits non-zero with a message naming the variable.
- [ ] **Started message:** on start, the service records the Watch Start from its clock and publishes `🟢 immich-tg started, watching uploads from <Watch Start>` to the Log Channel.
- [ ] **Test setup:** the service can be built from config plus an injectable clock and a poll trigger.
- [ ] **Fakes:** a reusable fake Immich and fake Telegram Bot API (`httptest`) exist. The fake Telegram records every call (method, chat ID and all fields, including multipart parts).
- [ ] **First test:** the started message goes to the Log Channel.
- [ ] **Dockerfile:** a multi-stage build producing a static binary on a minimal non-root base image that includes CA certificates.
- [ ] **`docker-compose.yml`:**
  - one service
  - `env_file: .env`
  - `restart: unless-stopped`
  - joins the external `telegram-bot-api` network
  - no ports
  - no volumes
- [ ] **`.env.example`:** documents every variable, including the typical values `http://telegram-bot-api:8081` and Immich's LAN URL.
- [ ] Logs are structured and go to stdout.
