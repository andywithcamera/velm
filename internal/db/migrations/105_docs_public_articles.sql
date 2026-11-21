-- Public docs (#372): an article may be flagged public, which makes it readable
-- by anyone holding the link, with no session. The flag is a boolean column and
-- not a tag, because a tag is a string a typo silently defeats: `publik` would
-- publish nothing and look exactly like a working setup. A column is also what
-- the reader's access decision reads, so the gate cannot disagree with the UI.
ALTER TABLE _docs_article
	ADD COLUMN IF NOT EXISTS is_public BOOLEAN NOT NULL DEFAULT FALSE;

-- The guest catalog lists only public published articles, so the index is
-- partial: it covers the one query guests run and costs nothing for the rest.
CREATE INDEX IF NOT EXISTS idx_docs_article_public
	ON _docs_article(is_public)
	WHERE is_public = TRUE;

-- The landing target for visitors without a session. Empty means "login",
-- which is the historical behaviour, so no default row is seeded: an install
-- that never sets this property keeps redirecting to the login page.
COMMENT ON COLUMN _docs_article.is_public IS
	'Article is readable by unauthenticated visitors (docs public flag, #372).';
