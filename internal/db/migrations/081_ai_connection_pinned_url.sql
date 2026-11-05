-- Bind a connection's credential to the destination it was issued for.
--
-- The API attaches the decrypted key to a request for the provider's base_url,
-- and base_url lives on _ai_provider, shared by every connection attached to
-- it. An admin editing one provider row could therefore redirect the keys of
-- every connection on that provider, and a second admin could receive the
-- first admin's keys in plaintext. Pinning the destination per connection
-- means changing a provider endpoint can no longer move an existing key: the
-- key only travels to the URL that was in force when it was entered.
--
-- Backfill runs at deploy time, before any later edit to _ai_provider can take
-- effect, so existing rows pin the destination that was legitimate when this
-- migration ran. A drift check in the sync path refuses rather than silently
-- re-pins, so a mismatch surfaces instead of being absorbed.

ALTER TABLE _ai_connection
	ADD COLUMN IF NOT EXISTS pinned_base_url TEXT NOT NULL DEFAULT '';

UPDATE _ai_connection c
SET pinned_base_url = p.base_url
FROM _ai_provider p
WHERE c.provider_id = p._id
  AND c.pinned_base_url = '';

COMMENT ON COLUMN _ai_connection.pinned_base_url IS
	'Provider base_url in force when the key was last entered. The key is only ever sent here; changing _ai_provider.base_url does not move it.';
