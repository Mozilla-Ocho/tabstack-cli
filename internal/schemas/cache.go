package schemas

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// IndexCacheName is the per-store cache of the library manifest, used to avoid
// refetching index.json on every list/pull/status.
const IndexCacheName = ".index-cache.json"

type cachedIndex struct {
	FetchedAt time.Time `json:"fetched_at"`
	Index     Index     `json:"index"`
}

// CachedIndex returns the library index, using a per-store cache file so we do
// not refetch index.json within ttl. refresh forces a fetch. If the network
// fetch fails but a (possibly stale) cache exists, the cache is returned so the
// commands keep working offline.
func CachedIndex(ctx context.Context, f *Fetcher, dir string, ttl time.Duration, refresh bool) (Index, error) {
	path := LocalPath(dir, IndexCacheName)

	var cache cachedIndex
	haveCache := false
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &cache) == nil && cache.Index.Schemas != nil {
			haveCache = true
		}
	}

	if !refresh && haveCache && time.Since(cache.FetchedAt) < ttl {
		return cache.Index, nil
	}

	idx, err := f.Index(ctx)
	if err != nil {
		if haveCache {
			return cache.Index, nil // offline fallback
		}
		return Index{}, err
	}

	_ = writeIndexCache(path, idx) // best effort; a cache miss is not fatal
	return idx, nil
}

func writeIndexCache(path string, idx Index) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cachedIndex{FetchedAt: time.Now().UTC(), Index: idx}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
