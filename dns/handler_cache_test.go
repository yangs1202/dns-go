package dns

import (
	"dns-go/model"
	"testing"
)

func TestReconfigureCacheStopsOldCacheAndKeepsNewCacheUsable(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	oldCache := handler.GetCache()
	handler.ReconfigureCache(&model.CacheSettings{
		MaxSize:         10,
		DefaultTTL:      120,
		NegativeTTL:     30,
		PrefetchTrigger: 0.8,
	})

	select {
	case <-oldCache.doneCh:
	default:
		t.Fatal("expected old cache cleanup goroutine to stop")
	}

	newCache := handler.GetCache()
	if newCache == nil {
		t.Fatal("expected new cache")
	}
	if newCache == oldCache {
		t.Fatal("expected cache to be replaced")
	}
	newCache.Set("example.com.", "A", nil, 0, true)
	if _, ok := newCache.Get("example.com.", "A"); !ok {
		t.Fatal("expected reconfigured cache to remain usable")
	}
}
