package adblock

import (
	"database/sql"
	"dns-go/model"
	"dns-go/storage"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type mockSyncStorage struct {
	sources          []*model.AdblockSource
	domains          map[int64][]string
	getSourceErr     error
	updateSourceErr  error
	addDomainErr     error
	removeDomainErr  error
	enabledSourceErr error
	addedDomains     []string
	removedSourceIDs []int64
	updatedSources   []*model.AdblockSource
}

func (m *mockSyncStorage) GetEnabledAdblockSources() ([]*model.AdblockSource, error) {
	if m.enabledSourceErr != nil {
		return nil, m.enabledSourceErr
	}
	var enabled []*model.AdblockSource
	for _, src := range m.sources {
		if src.Enabled {
			enabled = append(enabled, src)
		}
	}
	return enabled, nil
}

func (m *mockSyncStorage) GetAdblockSource(id int64) (*model.AdblockSource, error) {
	if m.getSourceErr != nil {
		return nil, m.getSourceErr
	}
	for _, src := range m.sources {
		if src.ID == id {
			return src, nil
		}
	}
	return nil, nil
}

func (m *mockSyncStorage) UpdateAdblockSource(source *model.AdblockSource) error {
	if m.updateSourceErr != nil {
		return m.updateSourceErr
	}
	m.updatedSources = append(m.updatedSources, source)
	for i, src := range m.sources {
		if src.ID == source.ID {
			m.sources[i] = source
			return nil
		}
	}
	return nil
}

func (m *mockSyncStorage) AddBlockedDomain(sourceID int64, domain string) error {
	if m.addDomainErr != nil {
		return m.addDomainErr
	}
	m.addedDomains = append(m.addedDomains, domain)
	if m.domains == nil {
		m.domains = make(map[int64][]string)
	}
	m.domains[sourceID] = append(m.domains[sourceID], domain)
	return nil
}

func (m *mockSyncStorage) AddBlockedDomainsBatch(sourceID int64, domains []string) error {
	if m.addDomainErr != nil {
		return m.addDomainErr
	}
	m.addedDomains = append(m.addedDomains, domains...)
	if m.domains == nil {
		m.domains = make(map[int64][]string)
	}
	m.domains[sourceID] = append(m.domains[sourceID], domains...)
	return nil
}

func (m *mockSyncStorage) RemoveBlockedDomains(sourceID int64) error {
	if m.removeDomainErr != nil {
		return m.removeDomainErr
	}
	m.removedSourceIDs = append(m.removedSourceIDs, sourceID)
	delete(m.domains, sourceID)
	return nil
}

func (m *mockSyncStorage) ListBlockedDomains() ([]string, error) {
	var all []string
	for _, domains := range m.domains {
		all = append(all, domains...)
	}
	return all, nil
}

func (m *mockSyncStorage) IsBlocked(domain string) (bool, error) {
	return false, nil
}

type mockSyncLoader struct {
	rules        []string
	lastModified string
	downloadErr  error
	downloadCalls int32
}

func (m *mockSyncLoader) Download(url, lastModified string) ([]string, string, error) {
	atomic.AddInt32(&m.downloadCalls, 1)
	if m.downloadErr != nil {
		return nil, "", m.downloadErr
	}
	return m.rules, m.lastModified, nil
}

func (m *mockSyncLoader) ParseRules(content string) []string {
	return m.rules
}

type mockSyncFilter struct {
	rebuildCalls int32
	rebuildErr   error
}

func (m *mockSyncFilter) Rebuild() error {
	atomic.AddInt32(&m.rebuildCalls, 1)
	return m.rebuildErr
}

func TestNewSyncer(t *testing.T) {
	storage := &mockSyncStorage{}
	loader := &mockSyncLoader{}
	filter := &mockSyncFilter{}
	interval := 1 * time.Hour

	syncer := NewSyncer(storage, loader, filter, interval)

	if syncer == nil {
		t.Fatal("Expected syncer to be created")
	}
	if syncer.storage != storage {
		t.Error("Storage not set correctly")
	}
	if syncer.loader != loader {
		t.Error("Loader not set correctly")
	}
	if syncer.filter != filter {
		t.Error("Filter not set correctly")
	}
	if syncer.interval != interval {
		t.Error("Interval not set correctly")
	}
	if syncer.stopCh == nil {
		t.Error("Stop channel not initialized")
	}
}

func TestSyncer_SyncAll(t *testing.T) {
	storage := &mockSyncStorage{
		sources: []*model.AdblockSource{
			{
				ID:      1,
				Name:    "Source 1",
				URL:     "http://example.com/filter1.txt",
				Enabled: true,
			},
			{
				ID:      2,
				Name:    "Source 2",
				URL:     "http://example.com/filter2.txt",
				Enabled: true,
			},
			{
				ID:      3,
				Name:    "Source 3 (Disabled)",
				URL:     "http://example.com/filter3.txt",
				Enabled: false,
			},
		},
	}

	loader := &mockSyncLoader{
		rules:        []string{"example.com", "test.com"},
		lastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
	}

	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

	err := syncer.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll() error = %v", err)
	}

	// Should download from 2 enabled sources
	if atomic.LoadInt32(&loader.downloadCalls) != 2 {
		t.Errorf("Expected 2 download calls, got %d", loader.downloadCalls)
	}

	// Should rebuild filter once at the end
	if atomic.LoadInt32(&filter.rebuildCalls) != 1 {
		t.Errorf("Expected 1 rebuild call, got %d", filter.rebuildCalls)
	}

	// Check if domains were added
	if len(storage.addedDomains) != 4 { // 2 domains × 2 sources
		t.Errorf("Expected 4 domains added, got %d", len(storage.addedDomains))
	}

	// Check if sources were updated
	if len(storage.updatedSources) != 2 {
		t.Errorf("Expected 2 sources updated, got %d", len(storage.updatedSources))
	}
}

func TestSyncer_SyncAll_NoEnabledSources(t *testing.T) {
	storage := &mockSyncStorage{
		sources: []*model.AdblockSource{
			{
				ID:      1,
				Name:    "Source 1 (Disabled)",
				URL:     "http://example.com/filter.txt",
				Enabled: false,
			},
		},
	}

	loader := &mockSyncLoader{}
	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

	err := syncer.SyncAll()
	if err != nil {
		t.Fatalf("SyncAll() error = %v", err)
	}

	// Should not download anything
	if atomic.LoadInt32(&loader.downloadCalls) != 0 {
		t.Errorf("Expected 0 download calls, got %d", loader.downloadCalls)
	}

	// Should still rebuild filter
	if atomic.LoadInt32(&filter.rebuildCalls) != 1 {
		t.Errorf("Expected 1 rebuild call, got %d", filter.rebuildCalls)
	}
}

func TestSyncer_SyncAll_StorageError(t *testing.T) {
	storage := &mockSyncStorage{
		enabledSourceErr: errors.New("database error"),
	}

	loader := &mockSyncLoader{}
	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

	err := syncer.SyncAll()
	if err == nil {
		t.Error("Expected error from SyncAll when storage fails")
	}
}

func TestSyncer_SyncSource(t *testing.T) {
	storage := &mockSyncStorage{
		sources: []*model.AdblockSource{
			{
				ID:      1,
				Name:    "Test Source",
				URL:     "http://example.com/filter.txt",
				Enabled: true,
			},
		},
	}

	loader := &mockSyncLoader{
		rules:        []string{"example.com", "test.com", "blocked.net"},
		lastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
	}

	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

	err := syncer.SyncSource(1)
	if err != nil {
		t.Fatalf("SyncSource() error = %v", err)
	}

	// Check download was called
	if atomic.LoadInt32(&loader.downloadCalls) != 1 {
		t.Errorf("Expected 1 download call, got %d", loader.downloadCalls)
	}

	// Check domains were removed first
	if len(storage.removedSourceIDs) != 1 || storage.removedSourceIDs[0] != 1 {
		t.Error("Expected domains to be removed before adding new ones")
	}

	// Check domains were added
	if len(storage.addedDomains) != 3 {
		t.Errorf("Expected 3 domains added, got %d", len(storage.addedDomains))
	}

	// Check source was updated
	if len(storage.updatedSources) != 1 {
		t.Errorf("Expected 1 source updated, got %d", len(storage.updatedSources))
	}

	// Check filter was rebuilt
	if atomic.LoadInt32(&filter.rebuildCalls) != 1 {
		t.Errorf("Expected 1 rebuild call, got %d", filter.rebuildCalls)
	}

	// Verify source fields were updated
	updatedSource := storage.updatedSources[0]
	if updatedSource.RuleCount != 3 {
		t.Errorf("Expected RuleCount = 3, got %d", updatedSource.RuleCount)
	}
	if updatedSource.LastModified.String != "Wed, 21 Oct 2015 07:28:00 GMT" {
		t.Errorf("Expected LastModified to be set, got %q", updatedSource.LastModified.String)
	}
	if !updatedSource.LastSync.Valid {
		t.Error("Expected LastSync to be set")
	}
}

func TestSyncer_SyncSource_NotModified(t *testing.T) {
	storage := &mockSyncStorage{
		sources: []*model.AdblockSource{
			{
				ID:      1,
				Name:    "Test Source",
				URL:     "http://example.com/filter.txt",
				Enabled: true,
				LastModified: sql.NullString{
					String: "Wed, 21 Oct 2015 07:28:00 GMT",
					Valid:  true,
				},
			},
		},
	}

	loader := &mockSyncLoader{
		rules:        nil, // nil means not modified
		lastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
	}

	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

	err := syncer.SyncSource(1)
	if err != nil {
		t.Fatalf("SyncSource() error = %v", err)
	}

	// Should not remove or add domains when not modified
	if len(storage.removedSourceIDs) != 0 {
		t.Error("Should not remove domains when not modified")
	}
	if len(storage.addedDomains) != 0 {
		t.Error("Should not add domains when not modified")
	}

	// Should still update source (LastSync)
	if len(storage.updatedSources) != 1 {
		t.Error("Should still update source metadata")
	}
}

func TestSyncer_SyncSource_DisabledSource(t *testing.T) {
	storage := &mockSyncStorage{
		sources: []*model.AdblockSource{
			{
				ID:      1,
				Name:    "Test Source",
				URL:     "http://example.com/filter.txt",
				Enabled: false,
			},
		},
	}

	loader := &mockSyncLoader{}
	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

	err := syncer.SyncSource(1)
	if err != nil {
		t.Fatalf("SyncSource() error = %v", err)
	}

	// Should not download when disabled
	if atomic.LoadInt32(&loader.downloadCalls) != 0 {
		t.Error("Should not download when source is disabled")
	}
}

func TestSyncer_SyncSource_NotFound(t *testing.T) {
	storage := &mockSyncStorage{
		sources: []*model.AdblockSource{},
	}

	loader := &mockSyncLoader{}
	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

	err := syncer.SyncSource(999)
	if err != nil {
		t.Errorf("Expected no error for non-existent source, got %v", err)
	}
}

func TestSyncer_StartStop(t *testing.T) {
	storage := &mockSyncStorage{
		sources: []*model.AdblockSource{},
	}

	loader := &mockSyncLoader{}
	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 100*time.Millisecond)

	// Start the syncer
	syncer.Start()

	// Wait for at least one sync cycle
	time.Sleep(250 * time.Millisecond)

	// Stop the syncer
	syncer.Stop()

	// Check that at least one rebuild happened
	rebuilds := atomic.LoadInt32(&filter.rebuildCalls)
	if rebuilds < 1 {
		t.Errorf("Expected at least 1 rebuild, got %d", rebuilds)
	}

	// Record rebuilds after stop
	rebuildsAfterStop := rebuilds

	// Wait a bit more and verify no more syncs happen
	time.Sleep(250 * time.Millisecond)
	finalRebuilds := atomic.LoadInt32(&filter.rebuildCalls)

	if finalRebuilds != rebuildsAfterStop {
		t.Errorf("Syncer should have stopped, but rebuilds increased from %d to %d",
			rebuildsAfterStop, finalRebuilds)
	}
}

func TestSyncer_WithRealHTTPServer(t *testing.T) {
	content := `! Test Filter List
||ads.example.com^
||tracker.test.com^
||malware.net^`

	lastModified := time.Now().UTC().Format(http.TimeFormat)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", lastModified)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(content))
	}))
	defer server.Close()

	storage := &mockSyncStorage{
		sources: []*model.AdblockSource{
			{
				ID:      1,
				Name:    "Test Source",
				URL:     server.URL,
				Enabled: true,
			},
		},
	}

	loader := NewLoader()
	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

	err := syncer.SyncSource(1)
	if err != nil {
		t.Fatalf("SyncSource() error = %v", err)
	}

	// Verify domains were added
	expectedDomains := []string{"ads.example.com", "tracker.test.com", "malware.net"}
	if len(storage.addedDomains) != len(expectedDomains) {
		t.Errorf("Expected %d domains, got %d", len(expectedDomains), len(storage.addedDomains))
	}

	// Verify last modified was set
	if len(storage.updatedSources) == 0 {
		t.Fatal("No sources were updated")
	}
	updatedSource := storage.updatedSources[0]
	if !updatedSource.LastModified.Valid {
		t.Error("LastModified should be set")
	}
}

func TestSyncer_WithRealStorage(t *testing.T) {
	// Setup test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	adblockStorage := storage.NewAdblockStorage(db)

	// Create test source
	source := &model.AdblockSource{
		Name:    "Test Source",
		URL:     "http://example.com/filter.txt",
		Enabled: true,
	}
	sourceID, err := adblockStorage.CreateAdblockSource(source)
	if err != nil {
		t.Fatalf("Failed to create source: %v", err)
	}

	// Setup mock loader
	loader := &mockSyncLoader{
		rules:        []string{"ads.example.com", "tracker.test.com"},
		lastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
	}

	// Setup mock filter
	filter := &mockSyncFilter{}

	syncer := NewSyncer(adblockStorage, loader, filter, 1*time.Hour)

	err = syncer.SyncSource(sourceID)
	if err != nil {
		t.Fatalf("SyncSource() error = %v", err)
	}

	// Verify domains were added to database
	domains, err := adblockStorage.ListBlockedDomains()
	if err != nil {
		t.Fatalf("Failed to list blocked domains: %v", err)
	}

	if len(domains) != 2 {
		t.Errorf("Expected 2 domains in database, got %d", len(domains))
	}

	// Verify source was updated
	updatedSource, err := adblockStorage.GetAdblockSource(sourceID)
	if err != nil {
		t.Fatalf("Failed to get source: %v", err)
	}

	if updatedSource.RuleCount != 2 {
		t.Errorf("Expected RuleCount = 2, got %d", updatedSource.RuleCount)
	}

	if !updatedSource.LastSync.Valid {
		t.Error("LastSync should be set")
	}
}

func TestSyncer_ErrorHandling(t *testing.T) {
	tests := []struct {
		name            string
		downloadErr     error
		removeDomainErr error
		addDomainErr    error
		updateSourceErr error
		rebuildErr      error
		expectErr       bool
	}{
		{
			name:        "Download error",
			downloadErr: errors.New("network error"),
			expectErr:   true,
		},
		{
			name:            "Remove domain error",
			removeDomainErr: errors.New("database error"),
			expectErr:       true,
		},
		{
			name:         "Add domain error",
			addDomainErr: errors.New("database error"),
			expectErr:    true,
		},
		{
			name:            "Update source error",
			updateSourceErr: errors.New("database error"),
			expectErr:       true,
		},
		{
			name:       "Rebuild error",
			rebuildErr: errors.New("rebuild failed"),
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := &mockSyncStorage{
				sources: []*model.AdblockSource{
					{
						ID:      1,
						Name:    "Test Source",
						URL:     "http://example.com/filter.txt",
						Enabled: true,
					},
				},
				removeDomainErr: tt.removeDomainErr,
				addDomainErr:    tt.addDomainErr,
				updateSourceErr: tt.updateSourceErr,
			}

			loader := &mockSyncLoader{
				rules:        []string{"example.com"},
				lastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
				downloadErr:  tt.downloadErr,
			}

			filter := &mockSyncFilter{
				rebuildErr: tt.rebuildErr,
			}

			syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

			err := syncer.SyncSource(1)
			if (err != nil) != tt.expectErr {
				t.Errorf("SyncSource() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func BenchmarkSyncer_SyncSource(b *testing.B) {
	// Generate many rules
	rules := make([]string, 10000)
	for i := 0; i < 10000; i++ {
		rules[i] = "domain" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + ".com"
	}

	storage := &mockSyncStorage{
		sources: []*model.AdblockSource{
			{
				ID:      1,
				Name:    "Test Source",
				URL:     "http://example.com/filter.txt",
				Enabled: true,
			},
		},
		domains: make(map[int64][]string),
	}

	loader := &mockSyncLoader{
		rules:        rules,
		lastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
	}

	filter := &mockSyncFilter{}

	syncer := NewSyncer(storage, loader, filter, 1*time.Hour)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = syncer.SyncSource(1)
	}
}
