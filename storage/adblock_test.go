package storage

import (
	"database/sql"
	"dns-go/model"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === AdblockCache Tests ===

func TestNewAdblockCache(t *testing.T) {
	ttl := 5 * time.Minute
	cache := NewAdblockCache(ttl)

	assert.NotNil(t, cache)
	assert.Equal(t, ttl, cache.ttl)
	assert.Nil(t, cache.sources)
	assert.True(t, cache.expiry.IsZero())
}

func TestAdblockCache_SetAndGet(t *testing.T) {
	cache := NewAdblockCache(5 * time.Minute)

	// Empty cache returns false
	sources, ok := cache.Get()
	assert.False(t, ok)
	assert.Nil(t, sources)

	// Set sources
	testSources := []*model.AdblockSource{
		{ID: 1, Name: "Source1", URL: "http://example.com/list1", Enabled: true},
		{ID: 2, Name: "Source2", URL: "http://example.com/list2", Enabled: false},
	}
	cache.Set(testSources)

	// Get returns cached sources
	sources, ok = cache.Get()
	assert.True(t, ok)
	require.Len(t, sources, 2)
	assert.Equal(t, int64(1), sources[0].ID)
	assert.Equal(t, "Source1", sources[0].Name)
	assert.Equal(t, int64(2), sources[1].ID)
	assert.Equal(t, "Source2", sources[1].Name)
}

func TestAdblockCache_TTLExpiry(t *testing.T) {
	cache := NewAdblockCache(1 * time.Second)

	testSources := []*model.AdblockSource{
		{ID: 1, Name: "Source1", URL: "http://example.com/list1", Enabled: true},
	}
	cache.Set(testSources)

	// Cache hit before expiry
	sources, ok := cache.Get()
	assert.True(t, ok)
	assert.NotNil(t, sources)

	// Wait for TTL to expire
	time.Sleep(1100 * time.Millisecond)

	// Cache miss after expiry
	sources, ok = cache.Get()
	assert.False(t, ok)
	assert.Nil(t, sources)
}

func TestAdblockCache_Invalidate(t *testing.T) {
	cache := NewAdblockCache(5 * time.Minute)

	testSources := []*model.AdblockSource{
		{ID: 1, Name: "Source1", URL: "http://example.com/list1", Enabled: true},
	}
	cache.Set(testSources)

	// Cache hit
	sources, ok := cache.Get()
	assert.True(t, ok)
	assert.NotNil(t, sources)

	// Invalidate
	cache.Invalidate()

	// Cache miss after invalidation
	sources, ok = cache.Get()
	assert.False(t, ok)
	assert.Nil(t, sources)
}

// === AdblockStorage Tests ===

func TestNewAdblockStorage(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	assert.NotNil(t, storage)
	assert.NotNil(t, storage.db)
	assert.NotNil(t, storage.cache)
	assert.Equal(t, 10*time.Minute, storage.cache.ttl)
}

func TestAdblockStorage_CreateAndGetSource(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Create source
	src := &model.AdblockSource{
		Name:    "AdGuard DNS",
		URL:     "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
		Enabled: true,
	}
	id, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	// Get source by ID
	got, err := storage.GetAdblockSource(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "AdGuard DNS", got.Name)
	assert.Equal(t, "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt", got.URL)
	assert.True(t, got.Enabled)
	assert.False(t, got.LastSync.Valid)
	assert.False(t, got.LastModified.Valid)
	assert.Equal(t, int64(0), got.RuleCount)
}

func TestAdblockStorage_GetNonExistentSource(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	got, err := storage.GetAdblockSource(9999)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestAdblockStorage_ListSources(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Empty list
	sources, err := storage.ListAdblockSources()
	require.NoError(t, err)
	assert.Empty(t, sources)

	// Create multiple sources
	src1 := &model.AdblockSource{Name: "Source1", URL: "http://example.com/list1", Enabled: true}
	src2 := &model.AdblockSource{Name: "Source2", URL: "http://example.com/list2", Enabled: false}
	src3 := &model.AdblockSource{Name: "Source3", URL: "http://example.com/list3", Enabled: true}

	_, err = storage.CreateAdblockSource(src1)
	require.NoError(t, err)
	_, err = storage.CreateAdblockSource(src2)
	require.NoError(t, err)
	_, err = storage.CreateAdblockSource(src3)
	require.NoError(t, err)

	// List all sources (ordered by id)
	sources, err = storage.ListAdblockSources()
	require.NoError(t, err)
	require.Len(t, sources, 3)
	assert.Equal(t, "Source1", sources[0].Name)
	assert.Equal(t, "Source2", sources[1].Name)
	assert.Equal(t, "Source3", sources[2].Name)
}

func TestAdblockStorage_ListSources_CachesResults(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	src := &model.AdblockSource{Name: "Source1", URL: "http://example.com/list1", Enabled: true}
	_, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// ListAdblockSources populates cache
	sources, err := storage.ListAdblockSources()
	require.NoError(t, err)
	require.Len(t, sources, 1)

	// Cache should be populated
	cachedSources, ok := storage.cache.Get()
	assert.True(t, ok)
	assert.Len(t, cachedSources, 1)
}

func TestAdblockStorage_GetEnabledSources_FromDB(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Create mixed enabled/disabled sources
	src1 := &model.AdblockSource{Name: "Enabled1", URL: "http://example.com/list1", Enabled: true}
	src2 := &model.AdblockSource{Name: "Disabled1", URL: "http://example.com/list2", Enabled: false}
	src3 := &model.AdblockSource{Name: "Enabled2", URL: "http://example.com/list3", Enabled: true}

	_, err := storage.CreateAdblockSource(src1)
	require.NoError(t, err)
	_, err = storage.CreateAdblockSource(src2)
	require.NoError(t, err)
	_, err = storage.CreateAdblockSource(src3)
	require.NoError(t, err)

	// Cache is invalidated by CreateAdblockSource, so this goes to DB
	enabled, err := storage.GetEnabledAdblockSources()
	require.NoError(t, err)
	require.Len(t, enabled, 2)
	assert.Equal(t, "Enabled1", enabled[0].Name)
	assert.Equal(t, "Enabled2", enabled[1].Name)
}

func TestAdblockStorage_GetEnabledSources_FromCache(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Create mixed sources
	src1 := &model.AdblockSource{Name: "Enabled1", URL: "http://example.com/list1", Enabled: true}
	src2 := &model.AdblockSource{Name: "Disabled1", URL: "http://example.com/list2", Enabled: false}
	src3 := &model.AdblockSource{Name: "Enabled2", URL: "http://example.com/list3", Enabled: true}

	_, err := storage.CreateAdblockSource(src1)
	require.NoError(t, err)
	_, err = storage.CreateAdblockSource(src2)
	require.NoError(t, err)
	_, err = storage.CreateAdblockSource(src3)
	require.NoError(t, err)

	// Populate cache via ListAdblockSources
	_, err = storage.ListAdblockSources()
	require.NoError(t, err)

	// GetEnabledAdblockSources should use cache and filter
	enabled, err := storage.GetEnabledAdblockSources()
	require.NoError(t, err)
	require.Len(t, enabled, 2)
	assert.Equal(t, "Enabled1", enabled[0].Name)
	assert.Equal(t, "Enabled2", enabled[1].Name)
}

func TestAdblockStorage_UpdateSource(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Create source
	src := &model.AdblockSource{Name: "Original", URL: "http://example.com/list1", Enabled: true}
	id, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Update source
	now := time.Now()
	updated := &model.AdblockSource{
		ID:           id,
		Name:         "Updated",
		URL:          "http://example.com/list2",
		Enabled:      false,
		LastSync:     sql.NullTime{Time: now, Valid: true},
		LastModified: sql.NullString{String: "etag123", Valid: true},
		RuleCount:    1500,
	}
	err = storage.UpdateAdblockSource(updated)
	require.NoError(t, err)

	// Verify update
	got, err := storage.GetAdblockSource(id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Updated", got.Name)
	assert.Equal(t, "http://example.com/list2", got.URL)
	assert.False(t, got.Enabled)
	assert.True(t, got.LastSync.Valid)
	assert.True(t, got.LastModified.Valid)
	assert.Equal(t, "etag123", got.LastModified.String)
	assert.Equal(t, int64(1500), got.RuleCount)
}

func TestAdblockStorage_UpdateNonExistentSource(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	src := &model.AdblockSource{
		ID:   9999,
		Name: "NonExistent",
		URL:  "http://example.com/list1",
	}
	err := storage.UpdateAdblockSource(src)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "adblock source")
}

func TestAdblockStorage_DeleteSource(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Create source
	src := &model.AdblockSource{Name: "ToDelete", URL: "http://example.com/list1", Enabled: true}
	id, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Delete source
	err = storage.DeleteAdblockSource(id)
	require.NoError(t, err)

	// Verify deletion
	got, err := storage.GetAdblockSource(id)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestAdblockStorage_DeleteNonExistentSource(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	err := storage.DeleteAdblockSource(9999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "adblock source")
}

// === Blocked Domains Tests ===

func TestAdblockStorage_AddAndCheckBlockedDomain(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Create source first
	src := &model.AdblockSource{Name: "TestSource", URL: "http://example.com/list1", Enabled: true}
	sourceID, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Add blocked domain
	err = storage.AddBlockedDomain(sourceID, "doubleclick.net")
	require.NoError(t, err)

	// Check if domain is blocked
	blocked, err := storage.IsBlocked("doubleclick.net")
	require.NoError(t, err)
	assert.True(t, blocked)

	// Check non-blocked domain
	blocked, err = storage.IsBlocked("google.com")
	require.NoError(t, err)
	assert.False(t, blocked)
}

func TestAdblockStorage_AddBlockedDomain_Normalization(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	src := &model.AdblockSource{Name: "TestSource", URL: "http://example.com/list1", Enabled: true}
	sourceID, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Add with trailing dot and mixed case
	err = storage.AddBlockedDomain(sourceID, "  DoubleClick.NET.  ")
	require.NoError(t, err)

	// Should be found with normalized query
	blocked, err := storage.IsBlocked("doubleclick.net")
	require.NoError(t, err)
	assert.True(t, blocked)
}

func TestAdblockStorage_AddBlockedDomainsBatch(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	src := &model.AdblockSource{Name: "TestSource", URL: "http://example.com/list1", Enabled: true}
	sourceID, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Batch add domains
	domains := []string{"ads.example.com", "tracking.example.com", "analytics.example.com"}
	err = storage.AddBlockedDomainsBatch(sourceID, domains)
	require.NoError(t, err)

	// Verify all domains are blocked
	for _, domain := range domains {
		blocked, err := storage.IsBlocked(domain)
		require.NoError(t, err)
		assert.True(t, blocked, "domain %s should be blocked", domain)
	}

	// Verify count
	count, err := storage.GetBlockedDomainCount()
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

func TestAdblockStorage_AddBlockedDomainsBatch_Duplicate(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	src := &model.AdblockSource{Name: "TestSource", URL: "http://example.com/list1", Enabled: true}
	sourceID, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Add domains with duplicates (INSERT OR IGNORE)
	domains := []string{"ads.example.com", "ads.example.com", "tracking.example.com"}
	err = storage.AddBlockedDomainsBatch(sourceID, domains)
	require.NoError(t, err)

	count, err := storage.GetBlockedDomainCount()
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestAdblockStorage_RemoveBlockedDomains(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	src := &model.AdblockSource{Name: "TestSource", URL: "http://example.com/list1", Enabled: true}
	sourceID, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Add domains
	domains := []string{"ads.example.com", "tracking.example.com"}
	err = storage.AddBlockedDomainsBatch(sourceID, domains)
	require.NoError(t, err)

	// Remove all domains for this source
	err = storage.RemoveBlockedDomains(sourceID)
	require.NoError(t, err)

	// Verify domains are removed
	count, err := storage.GetBlockedDomainCount()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestAdblockStorage_ListBlockedDomains(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	src := &model.AdblockSource{Name: "TestSource", URL: "http://example.com/list1", Enabled: true}
	sourceID, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Empty list
	domains, err := storage.ListBlockedDomains()
	require.NoError(t, err)
	assert.Empty(t, domains)

	// Add domains
	err = storage.AddBlockedDomainsBatch(sourceID, []string{"ads.example.com", "tracking.example.com"})
	require.NoError(t, err)

	// List domains
	domains, err = storage.ListBlockedDomains()
	require.NoError(t, err)
	assert.Len(t, domains, 2)
}

func TestAdblockStorage_GetBlockedDomainCount(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Zero count
	count, err := storage.GetBlockedDomainCount()
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	src := &model.AdblockSource{Name: "TestSource", URL: "http://example.com/list1", Enabled: true}
	sourceID, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Add domains
	err = storage.AddBlockedDomainsBatch(sourceID, []string{"a.com", "b.com", "c.com"})
	require.NoError(t, err)

	count, err = storage.GetBlockedDomainCount()
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

// === Stats Tests ===

func TestAdblockStorage_RecordBlockedQuery(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Record blocked queries
	err := storage.RecordBlockedQuery("ads.example.com", "192.168.1.1")
	require.NoError(t, err)

	err = storage.RecordBlockedQuery("ads.example.com", "192.168.1.2")
	require.NoError(t, err)

	err = storage.RecordBlockedQuery("tracking.example.com", "192.168.1.1")
	require.NoError(t, err)
}

func TestAdblockStorage_GetBlockedStats(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Empty stats
	stats, err := storage.GetBlockedStats(10)
	require.NoError(t, err)
	assert.Empty(t, stats)

	// Record queries
	for i := 0; i < 5; i++ {
		err = storage.RecordBlockedQuery("ads.example.com", "192.168.1.1")
		require.NoError(t, err)
	}
	for i := 0; i < 3; i++ {
		err = storage.RecordBlockedQuery("tracking.example.com", "192.168.1.1")
		require.NoError(t, err)
	}
	err = storage.RecordBlockedQuery("analytics.example.com", "192.168.1.1")
	require.NoError(t, err)

	// Get stats ordered by count DESC
	stats, err = storage.GetBlockedStats(10)
	require.NoError(t, err)
	require.Len(t, stats, 3)
	assert.Equal(t, "ads.example.com", stats[0].Domain)
	assert.Equal(t, int64(5), stats[0].Count)
	assert.Equal(t, "tracking.example.com", stats[1].Domain)
	assert.Equal(t, int64(3), stats[1].Count)
	assert.Equal(t, "analytics.example.com", stats[2].Domain)
	assert.Equal(t, int64(1), stats[2].Count)
}

func TestAdblockStorage_GetBlockedStats_WithLimit(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Record queries for multiple domains
	for i := 0; i < 5; i++ {
		err := storage.RecordBlockedQuery("ads.example.com", "192.168.1.1")
		require.NoError(t, err)
	}
	for i := 0; i < 3; i++ {
		err := storage.RecordBlockedQuery("tracking.example.com", "192.168.1.1")
		require.NoError(t, err)
	}
	err := storage.RecordBlockedQuery("analytics.example.com", "192.168.1.1")
	require.NoError(t, err)

	// Limit to 2
	stats, err := storage.GetBlockedStats(2)
	require.NoError(t, err)
	assert.Len(t, stats, 2)
}

func TestAdblockStorage_GetBlockedStats_DefaultLimit(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Zero or negative limit defaults to 10
	stats, err := storage.GetBlockedStats(0)
	require.NoError(t, err)
	assert.Empty(t, stats)

	stats, err = storage.GetBlockedStats(-1)
	require.NoError(t, err)
	assert.Empty(t, stats)
}

// === normalizeDomain Tests ===

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com", "example.com"},
		{"Example.COM", "example.com"},
		{"  example.com  ", "example.com"},
		{"example.com.", "example.com"},
		{"  EXAMPLE.COM.  ", "example.com"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeDomain(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// === Error path tests (using closed DB) ===

func TestAdblockStorage_GetAdblockSource_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetAdblockSource(1)
	assert.Error(t, err)
}

func TestAdblockStorage_ListAdblockSources_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Reader.Close()

	_, err := storage.ListAdblockSources()
	assert.Error(t, err)
}

func TestAdblockStorage_GetEnabledAdblockSources_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetEnabledAdblockSources()
	assert.Error(t, err)
}

func TestAdblockStorage_CreateAdblockSource_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Writer.Close()

	src := &model.AdblockSource{Name: "Test", URL: "http://example.com", Enabled: true}
	_, err := storage.CreateAdblockSource(src)
	assert.Error(t, err)
}

func TestAdblockStorage_UpdateAdblockSource_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Writer.Close()

	src := &model.AdblockSource{ID: 1, Name: "Test", URL: "http://example.com", Enabled: true}
	err := storage.UpdateAdblockSource(src)
	assert.Error(t, err)
}

func TestAdblockStorage_DeleteAdblockSource_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Writer.Close()

	err := storage.DeleteAdblockSource(1)
	assert.Error(t, err)
}

func TestAdblockStorage_AddBlockedDomain_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Writer.Close()

	err := storage.AddBlockedDomain(1, "example.com")
	assert.Error(t, err)
}

func TestAdblockStorage_AddBlockedDomainsBatch_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Writer.Close()

	err := storage.AddBlockedDomainsBatch(1, []string{"a.com", "b.com"})
	assert.Error(t, err)
}

func TestAdblockStorage_RemoveBlockedDomains_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Writer.Close()

	err := storage.RemoveBlockedDomains(1)
	assert.Error(t, err)
}

func TestAdblockStorage_IsBlocked_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Reader.Close()

	_, err := storage.IsBlocked("example.com")
	assert.Error(t, err)
}

func TestAdblockStorage_GetBlockedDomainCount_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetBlockedDomainCount()
	assert.Error(t, err)
}

func TestAdblockStorage_ListBlockedDomains_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Reader.Close()

	_, err := storage.ListBlockedDomains()
	assert.Error(t, err)
}

func TestAdblockStorage_RecordBlockedQuery_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Writer.Close()

	err := storage.RecordBlockedQuery("example.com", "192.168.1.1")
	assert.Error(t, err)
}

func TestAdblockStorage_GetBlockedStats_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetBlockedStats(10)
	assert.Error(t, err)
}

// === Cache invalidation on mutations ===

func TestAdblockStorage_CreateInvalidatesCache(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	// Populate cache
	src1 := &model.AdblockSource{Name: "Source1", URL: "http://example.com/list1", Enabled: true}
	_, err := storage.CreateAdblockSource(src1)
	require.NoError(t, err)

	_, err = storage.ListAdblockSources()
	require.NoError(t, err)

	// Cache should be populated
	_, ok := storage.cache.Get()
	assert.True(t, ok)

	// Creating a new source should invalidate cache
	src2 := &model.AdblockSource{Name: "Source2", URL: "http://example.com/list2", Enabled: true}
	_, err = storage.CreateAdblockSource(src2)
	require.NoError(t, err)

	_, ok = storage.cache.Get()
	assert.False(t, ok)
}

func TestAdblockStorage_UpdateInvalidatesCache(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	src := &model.AdblockSource{Name: "Source1", URL: "http://example.com/list1", Enabled: true}
	id, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Populate cache
	_, err = storage.ListAdblockSources()
	require.NoError(t, err)

	_, ok := storage.cache.Get()
	assert.True(t, ok)

	// Update should invalidate cache
	updated := &model.AdblockSource{ID: id, Name: "Updated", URL: "http://example.com/list2", Enabled: true}
	err = storage.UpdateAdblockSource(updated)
	require.NoError(t, err)

	_, ok = storage.cache.Get()
	assert.False(t, ok)
}

func TestAdblockStorage_DeleteInvalidatesCache(t *testing.T) {
	db := setupTestDB(t)
	storage := NewAdblockStorage(db)

	src := &model.AdblockSource{Name: "Source1", URL: "http://example.com/list1", Enabled: true}
	id, err := storage.CreateAdblockSource(src)
	require.NoError(t, err)

	// Populate cache
	_, err = storage.ListAdblockSources()
	require.NoError(t, err)

	_, ok := storage.cache.Get()
	assert.True(t, ok)

	// Delete should invalidate cache
	err = storage.DeleteAdblockSource(id)
	require.NoError(t, err)

	_, ok = storage.cache.Get()
	assert.False(t, ok)
}
