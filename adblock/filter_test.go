package adblock

import (
	"dns-go/model"
	"dns-go/storage"
	"os"
	"path/filepath"
	"testing"

	"github.com/bits-and-blooms/bloom/v3"
)

type mockStorage struct {
	domains      []string
	blockedSet   map[string]bool
	listErr      error
	isBlockedErr error
}

func (m *mockStorage) ListBlockedDomains() ([]string, error) {
	return m.domains, m.listErr
}

func (m *mockStorage) IsBlocked(domain string) (bool, error) {
	if m.isBlockedErr != nil {
		return false, m.isBlockedErr
	}
	return m.blockedSet[normalizeDomain(domain)], nil
}

func (m *mockStorage) GetEnabledAdblockSources() ([]*model.AdblockSource, error) {
	return nil, nil
}

func (m *mockStorage) GetAdblockSource(id int64) (*model.AdblockSource, error) {
	return nil, nil
}

func (m *mockStorage) UpdateAdblockSource(source *model.AdblockSource) error {
	return nil
}

func (m *mockStorage) AddBlockedDomain(sourceID int64, domain string) error {
	return nil
}

func (m *mockStorage) RemoveBlockedDomains(sourceID int64) error {
	return nil
}

func TestNewFilter(t *testing.T) {
	mock := &mockStorage{
		domains:    []string{"example.com", "test.com"},
		blockedSet: map[string]bool{"example.com": true, "test.com": true},
	}

	filter := NewFilter(mock, true)
	if filter == nil {
		t.Fatal("Expected filter to be created")
	}
	if !filter.enabled {
		t.Error("Expected filter to be enabled")
	}
	if filter.bloom == nil {
		t.Error("Expected bloom filter to be initialized")
	}
}

func TestFilter_SetEnabled(t *testing.T) {
	mock := &mockStorage{
		domains:    []string{"example.com"},
		blockedSet: map[string]bool{"example.com": true},
	}

	filter := NewFilter(mock, true)
	if !filter.enabled {
		t.Error("Expected filter to be enabled initially")
	}

	filter.SetEnabled(false)
	if filter.enabled {
		t.Error("Expected filter to be disabled")
	}

	filter.SetEnabled(true)
	if !filter.enabled {
		t.Error("Expected filter to be enabled again")
	}
}

func TestFilter_IsBlocked(t *testing.T) {
	tests := []struct {
		name        string
		enabled     bool
		domains     []string
		blockedSet  map[string]bool
		query       string
		wantBlocked bool
		wantErr     bool
	}{
		{
			name:        "Blocked domain",
			enabled:     true,
			domains:     []string{"example.com", "test.com"},
			blockedSet:  map[string]bool{"example.com": true, "test.com": true},
			query:       "example.com",
			wantBlocked: true,
			wantErr:     false,
		},
		{
			name:        "Not blocked domain",
			enabled:     true,
			domains:     []string{"example.com"},
			blockedSet:  map[string]bool{"example.com": true},
			query:       "google.com",
			wantBlocked: false,
			wantErr:     false,
		},
		{
			name:        "Filter disabled",
			enabled:     false,
			domains:     []string{"example.com"},
			blockedSet:  map[string]bool{"example.com": true},
			query:       "example.com",
			wantBlocked: false,
			wantErr:     false,
		},
		{
			name:        "Domain with trailing dot",
			enabled:     true,
			domains:     []string{"example.com"},
			blockedSet:  map[string]bool{"example.com": true},
			query:       "example.com.",
			wantBlocked: true,
			wantErr:     false,
		},
		{
			name:        "Domain with uppercase",
			enabled:     true,
			domains:     []string{"example.com"},
			blockedSet:  map[string]bool{"example.com": true},
			query:       "EXAMPLE.COM",
			wantBlocked: true,
			wantErr:     false,
		},
		{
			name:        "Domain with spaces",
			enabled:     true,
			domains:     []string{"example.com"},
			blockedSet:  map[string]bool{"example.com": true},
			query:       "  example.com  ",
			wantBlocked: true,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockStorage{
				domains:    tt.domains,
				blockedSet: tt.blockedSet,
			}

			filter := NewFilter(mock, tt.enabled)
			blocked, err := filter.IsBlocked(tt.query)

			if (err != nil) != tt.wantErr {
				t.Errorf("IsBlocked() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if blocked != tt.wantBlocked {
				t.Errorf("IsBlocked() = %v, want %v", blocked, tt.wantBlocked)
			}
		})
	}
}

func TestFilter_IsBlocked_NilBloom(t *testing.T) {
	mock := &mockStorage{
		domains:    []string{},
		blockedSet: map[string]bool{},
	}

	filter := &Filter{
		storage: mock,
		enabled: true,
		bloom:   nil,
	}

	blocked, err := filter.IsBlocked("example.com")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
	if blocked {
		t.Error("Expected not blocked when bloom is nil")
	}
}

func TestFilter_Rebuild(t *testing.T) {
	mock := &mockStorage{
		domains:    []string{"example.com", "test.com", "blocked.net"},
		blockedSet: map[string]bool{"example.com": true, "test.com": true, "blocked.net": true},
	}

	filter := NewFilter(mock, true)

	// Change the mock data
	mock.domains = []string{"newdomain.com", "another.com"}
	mock.blockedSet = map[string]bool{"newdomain.com": true, "another.com": true}

	err := filter.Rebuild()
	if err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}

	// Old domains should not be in bloom filter anymore
	if filter.bloom.TestString("example.com") {
		t.Error("Old domain should not be in bloom filter after rebuild")
	}

	// New domains should be in bloom filter
	if !filter.bloom.TestString("newdomain.com") {
		t.Error("New domain should be in bloom filter after rebuild")
	}
}

func TestFilter_Rebuild_Error(t *testing.T) {
	mock := &mockStorage{
		domains:    nil,
		blockedSet: map[string]bool{},
		listErr:    os.ErrNotExist,
	}

	filter := &Filter{
		storage: mock,
		enabled: true,
	}

	err := filter.Rebuild()
	if err == nil {
		t.Error("Expected error from Rebuild when ListBlockedDomains fails")
	}
}

func TestNormalizeDomain(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"example.com", "example.com"},
		{"EXAMPLE.COM", "example.com"},
		{"  example.com  ", "example.com"},
		{"example.com.", "example.com"},
		{"  EXAMPLE.COM.  ", "example.com"},
		{"", ""},
		{"   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeDomain(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeDomain(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFilter_ConcurrentAccess(t *testing.T) {
	mock := &mockStorage{
		domains:    []string{"example.com", "test.com"},
		blockedSet: map[string]bool{"example.com": true, "test.com": true},
	}

	filter := NewFilter(mock, true)

	// Test concurrent reads and writes
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_, _ = filter.IsBlocked("example.com")
			}
			done <- true
		}()
	}

	go func() {
		for i := 0; i < 10; i++ {
			filter.SetEnabled(true)
			filter.SetEnabled(false)
			_ = filter.Rebuild()
		}
	}()

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestFilter_WithRealStorage(t *testing.T) {
	// Setup test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

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

	// Add blocked domains
	domains := []string{"ads.example.com", "tracker.test.com", "malware.net"}
	for _, domain := range domains {
		if err := adblockStorage.AddBlockedDomain(sourceID, domain); err != nil {
			t.Fatalf("Failed to add domain: %v", err)
		}
	}

	// Test filter
	filter := NewFilter(adblockStorage, true)

	for _, domain := range domains {
		blocked, err := filter.IsBlocked(domain)
		if err != nil {
			t.Errorf("IsBlocked(%s) error = %v", domain, err)
		}
		if !blocked {
			t.Errorf("Expected %s to be blocked", domain)
		}
	}

	// Test non-blocked domain
	blocked, err := filter.IsBlocked("google.com")
	if err != nil {
		t.Errorf("IsBlocked(google.com) error = %v", err)
	}
	if blocked {
		t.Error("Expected google.com to not be blocked")
	}
}

func BenchmarkFilter_IsBlocked(b *testing.B) {
	// Create a filter with 100k domains
	domains := make([]string, 100000)
	blockedSet := make(map[string]bool)
	for i := 0; i < 100000; i++ {
		domain := string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + ".example.com"
		domains[i] = domain
		blockedSet[domain] = true
	}

	mock := &mockStorage{
		domains:    domains,
		blockedSet: blockedSet,
	}

	filter := NewFilter(mock, true)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = filter.IsBlocked("aa.example.com")
	}
}

func BenchmarkBloomFilter_TestString(b *testing.B) {
	bf := bloom.NewWithEstimates(100000, 0.01)
	for i := 0; i < 100000; i++ {
		bf.AddString(string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + ".example.com")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bf.TestString("aa.example.com")
	}
}
