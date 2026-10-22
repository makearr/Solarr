-- Age eviction and budget eviction both walk the cache oldest first.
CREATE INDEX metadata_cache_by_age ON metadata_cache(fetched_at);
