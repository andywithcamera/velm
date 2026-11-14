-- Record what the push service actually answered for each endpoint.
--
-- Before this, a rejected send (4xx from APNs/FCM) was indistinguishable from
-- a delivered one: webpush-go returns the *http.Response verbatim, and Go's
-- client only errors on transport failure, so Send counted an HTTP 403 as a
-- success. The user saw "subscribed" and got nothing, with no way to tell
-- where it stopped. These columns make the last outcome readable from
-- /api/push/status instead of only from server logs (#344).
ALTER TABLE _user_push_subscription
    ADD COLUMN IF NOT EXISTS last_status   INTEGER,
    ADD COLUMN IF NOT EXISTS last_error    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_failure  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS failure_count INTEGER NOT NULL DEFAULT 0;
