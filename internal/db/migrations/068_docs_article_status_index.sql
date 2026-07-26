-- Speeds up the global published article listing (ORDER BY published_at DESC)
-- used on every docs catalog and reader page load.
CREATE INDEX IF NOT EXISTS idx_docs_article_status_published_at
    ON _docs_article(status, published_at DESC NULLS LAST);
