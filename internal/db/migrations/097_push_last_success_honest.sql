-- last_success meant "row touched", not "delivered".
--
-- Migration 087 gave the column `NOT NULL DEFAULT now()`, and
-- UpsertPushSubscription set `last_success = now()` on every re-subscribe. A
-- device that had never received a single push therefore reported a success at
-- the moment its row was written. On this instance that produced
-- lastSuccess=2026-11-14T11:23:24Z alongside lastStatus=403 BadJwtToken, and the
-- timestamp was read as evidence that an earlier send had worked (#350).
--
-- Make "never delivered" expressible, and record row changes separately so the
-- freshness of a subscription is still visible without lying about delivery.
ALTER TABLE _user_push_subscription
    ALTER COLUMN last_success DROP DEFAULT,
    ALTER COLUMN last_success DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Rows whose last_success is the insert-time default carry no delivery
-- information: clear it rather than leave a fabricated time.
UPDATE _user_push_subscription
SET last_success = NULL
WHERE last_success IS NOT NULL
  AND last_success <= created_at + INTERVAL '1 second';
