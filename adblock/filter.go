package adblock

import (
	"dns-go/metrics"
	"strings"
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
)

// AdblockStorageInterface defines the methods needed from storage
type AdblockStorageInterface interface {
	ListBlockedDomains() ([]string, error)
	IsBlocked(domain string) (bool, error)
}

type Filter struct {
	storage AdblockStorageInterface
	bloom   *bloom.BloomFilter
	mu      sync.RWMutex
	enabled bool
}

func NewFilter(storage AdblockStorageInterface, enabled bool) *Filter {
	f := &Filter{storage: storage, enabled: enabled}
	f.Rebuild()
	return f
}

func (f *Filter) SetEnabled(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled = enabled
}

func (f *Filter) IsBlocked(domain string) (bool, error) {
	f.mu.RLock()
	if !f.enabled || f.bloom == nil {
		f.mu.RUnlock()
		return false, nil
	}
	bloomFilter := f.bloom
	f.mu.RUnlock()

	domain = normalizeDomain(domain)
	if !bloomFilter.TestString(domain) {
		return false, nil
	}
	return f.storage.IsBlocked(domain)
}

func (f *Filter) Rebuild() error {
	domains, err := f.storage.ListBlockedDomains()
	if err != nil {
		return err
	}
	bf := bloom.NewWithEstimates(uint(len(domains)+1), 0.01)
	for _, domain := range domains {
		bf.AddString(domain)
	}

	f.mu.Lock()
	f.bloom = bf
	f.mu.Unlock()
	metrics.AdblockRulesTotal.Set(float64(len(domains)))
	return nil
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	domain = strings.ToLower(domain)
	domain = strings.TrimSuffix(domain, ".")
	return domain
}
