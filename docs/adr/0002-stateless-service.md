# Stateless service: downtime gaps are accepted

immich-tg persists nothing. The Watch Start is reset on every start, and which Videos are Waiting or Posted is tracked only in memory. Videos uploaded while the service is down are never posted, and a crash mid-post may lose that Post or, rarely, duplicate it. This was chosen deliberately over a persisted checkpoint with catch-up, to keep deployment to a single container with no volumes. Posts are fire-and-forget for the same reason: syncing Immich deletions to Telegram would need a durable Video→Post mapping.

## Consequences

- The started and stopped messages in the Log Channel are the only record of gaps.
