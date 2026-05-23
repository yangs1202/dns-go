package adblock

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Loader struct {
	client *http.Client
}

func NewLoader() *Loader {
	return &Loader{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (l *Loader) Download(url, lastModified string) ([]string, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil, lastModified, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("다운로드 실패: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	rules := l.ParseRules(string(body))

	modified := resp.Header.Get("Last-Modified")
	if modified == "" {
		modified = lastModified
	}

	return rules, modified, nil
}

func (l *Loader) ParseRules(content string) []string {
	rules := make([]string, 0)
	active := make(map[string]int)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		parsedRules, ok, err := parseRuleLines(scanner.Text())
		if err != nil || !ok {
			continue
		}

		for _, rule := range parsedRules {
			if rule.BadFilter {
				key := badFilterKey(rule)
				if idx, exists := active[key]; exists {
					rules[idx] = ""
					delete(active, key)
				}
				continue
			}

			if oldIdx, exists := active[rule.Raw]; exists {
				rules[oldIdx] = ""
			}
			active[rule.Raw] = len(rules)
			rules = append(rules, rule.Raw)
		}
	}

	compact := rules[:0]
	for _, rule := range rules {
		if rule == "" {
			continue
		}
		compact = append(compact, rule)
	}
	return compact
}
