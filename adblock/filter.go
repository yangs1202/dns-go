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
	rules   *compiledRules
	mu      sync.RWMutex
	enabled bool
}

type compiledRules struct {
	blockDomains          map[string]struct{}
	allowDomains          map[string]struct{}
	importantBlockDomains map[string]struct{}
	importantAllowDomains map[string]struct{}
	blockRules            []compiledRule
	allowRules            []compiledRule
	importantBlockRules   []compiledRule
	importantAllowRules   []compiledRule
}

func NewFilter(storage AdblockStorageInterface, enabled bool) *Filter {
	f := &Filter{storage: storage, enabled: enabled}
	_ = f.Rebuild()
	return f
}

func (f *Filter) SetEnabled(enabled bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enabled = enabled
}

func (f *Filter) IsBlocked(domain string) (bool, error) {
	f.mu.RLock()
	if !f.enabled || f.rules == nil {
		f.mu.RUnlock()
		return false, nil
	}
	rules := f.rules
	f.mu.RUnlock()

	domain = normalizeDomain(domain)
	return rules.isBlocked(domain), nil
}

func (f *Filter) Rebuild() error {
	storedRules, err := f.storage.ListBlockedDomains()
	if err != nil {
		return err
	}
	compiled := newCompiledRules()
	bf := bloom.NewWithEstimates(uint(len(storedRules)+1), 0.01)
	for _, raw := range storedRules {
		rule, ok, err := parseRuleLine(raw)
		if err != nil || !ok || rule.BadFilter {
			continue
		}
		compiled.add(rule)
		if rule.Kind == ruleKindDomain {
			bf.AddString(rule.Domain)
		}
	}

	f.mu.Lock()
	f.bloom = bf
	f.rules = compiled
	f.mu.Unlock()
	metrics.AdblockRulesTotal.Set(float64(len(storedRules)))
	return nil
}

func newCompiledRules() *compiledRules {
	return &compiledRules{
		blockDomains:          make(map[string]struct{}),
		allowDomains:          make(map[string]struct{}),
		importantBlockDomains: make(map[string]struct{}),
		importantAllowDomains: make(map[string]struct{}),
	}
}

func (r *compiledRules) add(rule *parsedRule) {
	compiled := compiledRule{
		raw:       rule.Raw,
		exception: rule.Exception,
		important: rule.Important,
		kind:      rule.Kind,
		domain:    rule.Domain,
		re:        rule.re,
	}

	if rule.Kind == ruleKindDomain {
		switch {
		case rule.Exception && rule.Important:
			r.importantAllowDomains[rule.Domain] = struct{}{}
		case rule.Exception:
			r.allowDomains[rule.Domain] = struct{}{}
		case rule.Important:
			r.importantBlockDomains[rule.Domain] = struct{}{}
		default:
			r.blockDomains[rule.Domain] = struct{}{}
		}
		return
	}

	switch {
	case rule.Exception && rule.Important:
		r.importantAllowRules = append(r.importantAllowRules, compiled)
	case rule.Exception:
		r.allowRules = append(r.allowRules, compiled)
	case rule.Important:
		r.importantBlockRules = append(r.importantBlockRules, compiled)
	default:
		r.blockRules = append(r.blockRules, compiled)
	}
}

func (r *compiledRules) isBlocked(qname string) bool {
	candidates := []string{
		qname,
		qname + ":80",
		qname + ":443",
		"http://" + qname,
		"http://" + qname + "/",
		"https://" + qname,
		"https://" + qname + "/",
	}

	if matchDomainMap(r.importantAllowDomains, qname) || matchRuleList(r.importantAllowRules, qname, candidates) {
		return false
	}
	if matchDomainMap(r.importantBlockDomains, qname) || matchRuleList(r.importantBlockRules, qname, candidates) {
		return true
	}
	if matchDomainMap(r.allowDomains, qname) || matchRuleList(r.allowRules, qname, candidates) {
		return false
	}
	return matchDomainMap(r.blockDomains, qname) || matchRuleList(r.blockRules, qname, candidates)
}

func matchDomainMap(domains map[string]struct{}, qname string) bool {
	for candidate := qname; candidate != ""; {
		if _, ok := domains[candidate]; ok {
			return true
		}
		idx := strings.Index(candidate, ".")
		if idx < 0 || idx == len(candidate)-1 {
			break
		}
		candidate = candidate[idx+1:]
	}
	return false
}

func matchRuleList(rules []compiledRule, qname string, candidates []string) bool {
	for _, rule := range rules {
		if ruleMatches(rule, qname, candidates) {
			return true
		}
	}
	return false
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	domain = strings.ToLower(domain)
	domain = strings.TrimSuffix(domain, ".")
	return domain
}
