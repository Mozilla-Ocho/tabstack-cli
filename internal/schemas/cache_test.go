package schemas

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// countingServer returns a fetcher and a pointer to the index.json hit count.
func countingServer(t *testing.T) (*Fetcher, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			atomic.AddInt32(&hits, 1)
			_, _ = w.Write([]byte(testIndex))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return NewFetcher(WithRawBase(srv.URL), WithHTTPClient(srv.Client())), &hits
}

func TestCachedIndexUsesCacheWithinTTL(t *testing.T) {
	dir := t.TempDir()
	f, hits := countingServer(t)
	ctx := context.Background()

	if _, err := CachedIndex(ctx, f, dir, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedIndex(ctx, f, dir, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("index fetched %d times, want 1 (second served from cache)", got)
	}
}

func TestCachedIndexRefreshForcesFetch(t *testing.T) {
	dir := t.TempDir()
	f, hits := countingServer(t)
	ctx := context.Background()

	if _, err := CachedIndex(ctx, f, dir, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedIndex(ctx, f, dir, time.Hour, true); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Errorf("index fetched %d times, want 2 (refresh bypasses cache)", got)
	}
}

func TestCachedIndexIgnoresCorruptCache(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(LocalPath(dir, IndexCacheName), []byte("{garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, hits := countingServer(t)
	idx, err := CachedIndex(context.Background(), f, dir, time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Schemas) != 3 {
		t.Errorf("got %d schemas, want 3", len(idx.Schemas))
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Error("corrupt cache should have triggered a fetch")
	}
}

func TestCachedIndexNoCacheNoNetwork(t *testing.T) {
	// No cache and a dead host: should surface the fetch error, not a cache hit.
	dead := NewFetcher(WithRawBase("http://127.0.0.1:0"))
	if _, err := CachedIndex(context.Background(), dead, t.TempDir(), time.Hour, false); err == nil {
		t.Error("expected error with no cache and no network")
	}
}

func TestCachedIndexOfflineFallback(t *testing.T) {
	dir := t.TempDir()
	f, _ := countingServer(t)
	ctx := context.Background()

	// Warm the cache, then point the fetcher at a dead host with a zero TTL so
	// it must refetch: the stale cache should still be returned.
	if _, err := CachedIndex(ctx, f, dir, time.Hour, false); err != nil {
		t.Fatal(err)
	}
	dead := NewFetcher(WithRawBase("http://127.0.0.1:0"))
	idx, err := CachedIndex(ctx, dead, dir, 0, false)
	if err != nil {
		t.Fatalf("expected offline fallback, got error: %v", err)
	}
	if len(idx.Schemas) != 3 {
		t.Errorf("got %d schemas from cache, want 3", len(idx.Schemas))
	}
}
