# 07 — Startup checks and graceful shutdown

**What to build:** A misconfigured deployment fails immediately, with a message that says exactly what's wrong, instead of failing at the first upload. `docker compose down` stops the service cleanly and announces it in the Log Channel.

See the spec: "Lifecycle" and "Supported Immich".

**Blocked by:** 01 — Walking skeleton.

**Status:** ready-for-agent

- [ ] **Checks run in order** before the started message; any failure exits non-zero with a clear message:
  1. Immich is reachable and its server version is ≥ 3.2.0.
  2. `GET /api/api-keys/me` `permissions` contains `all`, or every one of `user.read`, `asset.read`, `asset.view`, `asset.share` and `sharedLink.create`. The error names each missing scope.
  3. `GET /api/users/me` succeeds.
  4. `getMe` succeeds.
  5. For both the Video Channel and the Log Channel, `getChatMember` for the bot returns `status == "administrator"` with `can_post_messages == true`.
- [ ] **Graceful shutdown on SIGTERM or SIGINT:**
  - polling stops
  - the in-flight Video is finished or abandoned
  - `🔴 immich-tg stopped` is published to the Log Channel
  - the process exits 0
- [ ] **Tests:**
  - each check has a failing case with its message
  - a key with `all` passes
  - a key missing two scopes names both
  - shutdown produces the stopped message
