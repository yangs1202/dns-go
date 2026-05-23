package adblock

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

type ruleKind int

const (
	ruleKindDomain ruleKind = iota
	ruleKindDomainPattern
	ruleKindPattern
	ruleKindRegex
)

type parsedRule struct {
	Raw       string
	Pattern   string
	Kind      ruleKind
	Domain    string
	Exception bool
	Important bool
	BadFilter bool
	re        *regexp.Regexp
}

type compiledRule struct {
	raw       string
	exception bool
	important bool
	kind      ruleKind
	domain    string
	re        *regexp.Regexp
}

func parseRuleLine(line string) (*parsedRule, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "!") || strings.HasPrefix(line, "#") {
		return nil, false, nil
	}

	pattern, options := splitRuleOptions(line)
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, false, nil
	}

	rule := &parsedRule{
		Raw:       canonicalRule(pattern, options),
		Pattern:   strings.ToLower(pattern),
		Exception: strings.HasPrefix(pattern, "@@"),
		Important: hasOption(options, "important"),
		BadFilter: hasOption(options, "badfilter"),
	}
	if rule.Exception {
		rule.Pattern = strings.TrimPrefix(rule.Pattern, "@@")
	}

	if strings.HasPrefix(rule.Pattern, "/") && strings.HasSuffix(rule.Pattern, "/") && len(rule.Pattern) > 1 {
		rule.Kind = ruleKindRegex
		expr := strings.TrimSuffix(strings.TrimPrefix(rule.Pattern, "/"), "/")
		re, err := regexp.Compile("(?i)" + expr)
		if err != nil {
			return nil, false, fmt.Errorf("invalid adblock regex rule %q: %w", line, err)
		}
		rule.re = re
		return rule, true, nil
	}

	if strings.HasPrefix(rule.Pattern, "||") {
		domainPattern := normalizeDomainPattern(strings.TrimPrefix(rule.Pattern, "||"))
		if domainPattern == "" {
			return nil, false, nil
		}
		rule.Domain = domainPattern
		if strings.ContainsAny(domainPattern, "*") {
			rule.Kind = ruleKindDomainPattern
			re, err := regexp.Compile("(?i)(^|.*\\.)" + wildcardToRegexp(domainPattern) + "$")
			if err != nil {
				return nil, false, fmt.Errorf("invalid adblock domain wildcard rule %q: %w", line, err)
			}
			rule.re = re
			return rule, true, nil
		}
		if isPlainDomain(domainPattern) {
			rule.Kind = ruleKindDomain
			return rule, true, nil
		}
	}

	if looksLikeDomainPattern(rule.Pattern) {
		domainPattern := normalizeDomainPattern(rule.Pattern)
		rule.Domain = domainPattern
		if strings.Contains(domainPattern, "*") {
			rule.Kind = ruleKindDomainPattern
			re, err := regexp.Compile("(?i)(^|.*\\.)" + wildcardToRegexp(domainPattern) + "$")
			if err != nil {
				return nil, false, fmt.Errorf("invalid adblock hostname wildcard rule %q: %w", line, err)
			}
			rule.re = re
			return rule, true, nil
		}
		rule.Kind = ruleKindDomain
		return rule, true, nil
	}

	rule.Kind = ruleKindPattern
	re, err := regexp.Compile("(?i)" + adblockPatternToRegexp(rule.Pattern))
	if err != nil {
		return nil, false, fmt.Errorf("invalid adblock pattern rule %q: %w", line, err)
	}
	rule.re = re
	return rule, true, nil
}

func parseRuleLines(line string) ([]*parsedRule, bool, error) {
	if domains, ok := parseHostsLine(line); ok {
		rules := make([]*parsedRule, 0, len(domains))
		for _, domain := range domains {
			rule, parsed, err := parseRuleLine(domain)
			if err != nil {
				return nil, false, err
			}
			if parsed {
				rules = append(rules, rule)
			}
		}
		return rules, len(rules) > 0, nil
	}

	rule, ok, err := parseRuleLine(line)
	if err != nil || !ok {
		return nil, ok, err
	}
	return []*parsedRule{rule}, true, nil
}

func parseHostsLine(line string) ([]string, bool) {
	if idx := strings.IndexAny(line, "#!"); idx >= 0 {
		line = line[:idx]
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, false
	}

	ip := net.ParseIP(fields[0])
	if ip == nil {
		return nil, false
	}
	if !ip.IsLoopback() && !ip.IsUnspecified() {
		return nil, true
	}

	domains := make([]string, 0, len(fields)-1)
	for _, field := range fields[1:] {
		domain := normalizeDomain(field)
		if isPlainDomain(domain) {
			domains = append(domains, domain)
		}
	}
	return domains, true
}

func splitRuleOptions(line string) (string, []string) {
	if strings.HasPrefix(line, "/") {
		lastSlash := strings.LastIndex(line, "/")
		if lastSlash > 0 {
			if lastSlash == len(line)-1 {
				return line, nil
			}
			if line[lastSlash+1] == '$' {
				return line[:lastSlash+1], parseOptions(line[lastSlash+2:])
			}
			return line, nil
		}
	}

	idx := strings.Index(line, "$")
	if idx < 0 {
		return line, nil
	}
	return line[:idx], parseOptions(line[idx+1:])
}

func parseOptions(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	options := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.ToLower(part))
		part = strings.TrimPrefix(part, "~")
		if eq := strings.Index(part, "="); eq >= 0 {
			part = part[:eq]
		}
		if part != "" {
			options = append(options, part)
		}
	}
	return options
}

func hasOption(options []string, want string) bool {
	for _, option := range options {
		if option == want {
			return true
		}
	}
	return false
}

func canonicalRule(pattern string, options []string) string {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if len(options) == 0 {
		return pattern
	}

	kept := make([]string, 0, len(options))
	for _, option := range options {
		switch option {
		case "badfilter":
			continue
		case "important":
			kept = append(kept, option)
		}
	}
	if len(kept) == 0 {
		return pattern
	}
	return pattern + "$" + strings.Join(kept, ",")
}

func badFilterKey(rule *parsedRule) string {
	return canonicalRule(strings.TrimPrefix(rule.Pattern, "@@"), nil)
}

func normalizeDomainPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	pattern = strings.TrimSuffix(pattern, "|")
	pattern = strings.TrimSuffix(pattern, "^")
	pattern = strings.TrimSuffix(pattern, ".")
	return strings.ToLower(pattern)
}

func isPlainDomain(domain string) bool {
	if domain == "" || strings.ContainsAny(domain, "*^|/:") {
		return false
	}
	for _, r := range domain {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return strings.Contains(domain, ".")
}

func looksLikeDomainPattern(pattern string) bool {
	pattern = normalizeDomainPattern(pattern)
	if pattern == "" || strings.HasPrefix(pattern, ".") || !strings.Contains(pattern, ".") || strings.ContainsAny(pattern, "/:|") {
		return false
	}
	for _, r := range pattern {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '*' {
			continue
		}
		return false
	}
	return true
}

func wildcardToRegexp(pattern string) string {
	var b strings.Builder
	for _, r := range pattern {
		if r == '*' {
			b.WriteString(".*")
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(r)))
	}
	return b.String()
}

func adblockPatternToRegexp(pattern string) string {
	var b strings.Builder
	if strings.HasPrefix(pattern, "|") && !strings.HasPrefix(pattern, "||") {
		b.WriteString("^")
		pattern = strings.TrimPrefix(pattern, "|")
	}

	endAnchored := strings.HasSuffix(pattern, "|")
	if endAnchored {
		pattern = strings.TrimSuffix(pattern, "|")
	}

	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '^':
			b.WriteString("(?:[^A-Za-z0-9_.%-]|$)")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	if endAnchored {
		b.WriteString("$")
	}
	return b.String()
}

func ruleMatches(rule compiledRule, qname string, candidates []string) bool {
	switch rule.kind {
	case ruleKindDomain:
		return matchesDomainRule(qname, rule.domain)
	case ruleKindDomainPattern:
		return rule.re != nil && rule.re.MatchString(qname)
	case ruleKindPattern, ruleKindRegex:
		if rule.re == nil {
			return false
		}
		for _, candidate := range candidates {
			if rule.re.MatchString(candidate) {
				return true
			}
		}
	}
	return false
}

func matchesDomainRule(qname, ruleDomain string) bool {
	q := normalizeDomain(qname)
	r := normalizeDomain(ruleDomain)
	return q == r || strings.HasSuffix(q, "."+r)
}
