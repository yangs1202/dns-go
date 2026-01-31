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
	defer resp.Body.Close()

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
	var domains []string
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "!") {
			continue
		}
		if strings.Contains(line, "$") {
			line = strings.Split(line, "$")[0]
		}
		if strings.HasPrefix(line, "||") {
			line = strings.TrimPrefix(line, "||")
		}
		line = strings.TrimSuffix(line, "^")
		line = strings.TrimPrefix(line, "|")
		line = strings.TrimPrefix(line, "@@")
		if line == "" || strings.Contains(line, "/") {
			continue
		}
		domain := normalizeDomain(line)
		if domain == "" {
			continue
		}
		domains = append(domains, domain)
	}
	return domains
}
