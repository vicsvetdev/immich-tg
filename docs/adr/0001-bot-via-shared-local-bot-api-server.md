# Post via a bot through a shared local Bot API server

Videos are published by a Telegram bot, not a user account, and all Bot API calls go to a self-hosted `telegram-bot-api` server running in `--local` mode, which raises the upload limit from 50 MB to 2000 MB. A user account (MTProto) would have allowed uploads up to 2–4 GB with no extra server, but it needs an interactive phone login, a persisted session file that grants full access to a personal Telegram account, and it sits in a ToS grey area. The Bot API server is a separate compose project shared with other bots, so immich-tg reaches it only by URL and uploads files over HTTP multipart. It never passes local file paths, because that would need a shared filesystem.

## Consequences

- Videos whose Transcode is over 2 GB can never be uploaded and get a Link-only Post.
- The Bot API server and every bot stack join a pre-created external Docker network, `telegram-bot-api`, and immich-tg calls `http://telegram-bot-api:8081`. The server publishes no ports, which means all bots must run on the same Docker host. To reach it from another host, publish the port and change `TELEGRAM_API_URL`; no code change is needed.
- The bot has to be logged out of Telegram's cloud Bot API (`logOut`) once before the local server can use it.
