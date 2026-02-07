package adblock

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewLoader(t *testing.T) {
	loader := NewLoader()
	if loader == nil {
		t.Fatal("Expected loader to be created")
	}
	if loader.client == nil {
		t.Error("Expected HTTP client to be initialized")
	}
	if loader.client.Timeout != 30*time.Second {
		t.Errorf("Expected timeout to be 30s, got %v", loader.client.Timeout)
	}
}

func TestLoader_ParseRules(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []string
	}{
		{
			name: "Basic domain rules",
			content: `||example.com^
||test.com^
||blocked.net^`,
			expected: []string{"example.com", "test.com", "blocked.net"},
		},
		{
			name: "Rules with comments",
			content: `! This is a comment
||example.com^
! Another comment
||test.com^`,
			expected: []string{"example.com", "test.com"},
		},
		{
			name: "Rules with options",
			content: `||ads.example.com^$third-party
||tracker.com^$script,domain=example.com`,
			expected: []string{"ads.example.com", "tracker.com"},
		},
		{
			name: "Rules with various formats",
			content: `||example.com^
@@||whitelist.com^
example.net`,
			expected: []string{"example.com", "||whitelist.com", "example.net"},
		},
		{
			name: "Empty lines and spaces",
			content: `
||example.com^

  ||test.com^
`,
			expected: []string{"example.com", "test.com"},
		},
		{
			name: "Rules with paths (should be filtered out)",
			content: `||example.com^
||test.com/path/to/resource
||valid.net^`,
			expected: []string{"example.com", "valid.net"},
		},
		{
			name:     "Empty content",
			content:  "",
			expected: []string{},
		},
		{
			name: "Only comments",
			content: `! Comment 1
! Comment 2
! Comment 3`,
			expected: []string{},
		},
		{
			name: "Mixed case domains",
			content: `||EXAMPLE.COM^
||TeSt.CoM^
||blocked.NET^`,
			expected: []string{"example.com", "test.com", "blocked.net"},
		},
		{
			name: "Domains with trailing dots",
			content: `||example.com.^
||test.com.^`,
			expected: []string{"example.com", "test.com"},
		},
		{
			name: "Complex adblock rules",
			content: `! Title: Test Filter
! Homepage: https://example.com
||ads.example.com^
||tracker.test.com^$third-party,script
||malware.net^$popup
/banner.js
||cdn.ads.com^`,
			expected: []string{"ads.example.com", "tracker.test.com", "malware.net", "cdn.ads.com"},
		},
	}

	loader := NewLoader()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := loader.ParseRules(tt.content)

			if len(result) != len(tt.expected) {
				t.Errorf("ParseRules() returned %d domains, want %d\nGot: %v\nWant: %v",
					len(result), len(tt.expected), result, tt.expected)
				return
			}

			resultMap := make(map[string]bool)
			for _, domain := range result {
				resultMap[domain] = true
			}

			for _, expected := range tt.expected {
				if !resultMap[expected] {
					t.Errorf("Expected domain %q not found in result: %v", expected, result)
				}
			}
		})
	}
}

func TestLoader_Download(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		statusCode   int
		lastModified string
		reqLastMod   string
		expectRules  int
		expectErr    bool
		expectNotMod bool
	}{
		{
			name: "Successful download",
			content: `||example.com^
||test.com^
||blocked.net^`,
			statusCode:   http.StatusOK,
			lastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
			expectRules:  3,
			expectErr:    false,
		},
		{
			name:         "Not Modified",
			content:      "",
			statusCode:   http.StatusNotModified,
			lastModified: "Wed, 21 Oct 2015 07:28:00 GMT",
			reqLastMod:   "Wed, 21 Oct 2015 07:28:00 GMT",
			expectRules:  0,
			expectErr:    false,
			expectNotMod: true,
		},
		{
			name:        "Server Error",
			content:     "",
			statusCode:  http.StatusInternalServerError,
			expectRules: 0,
			expectErr:   true,
		},
		{
			name:        "Not Found",
			content:     "",
			statusCode:  http.StatusNotFound,
			expectRules: 0,
			expectErr:   true,
		},
		{
			name: "Download without Last-Modified header",
			content: `||example.com^
||test.com^`,
			statusCode:   http.StatusOK,
			lastModified: "",
			expectRules:  2,
			expectErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.reqLastMod != "" {
					ifModSince := r.Header.Get("If-Modified-Since")
					if ifModSince == tt.reqLastMod {
						w.WriteHeader(http.StatusNotModified)
						return
					}
				}

				if tt.lastModified != "" {
					w.Header().Set("Last-Modified", tt.lastModified)
				}
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.content))
			}))
			defer server.Close()

			loader := NewLoader()
			rules, lastMod, err := loader.Download(server.URL, tt.reqLastMod)

			if (err != nil) != tt.expectErr {
				t.Errorf("Download() error = %v, expectErr %v", err, tt.expectErr)
				return
			}

			if tt.expectNotMod {
				if rules != nil {
					t.Error("Expected nil rules for NotModified response")
				}
				if lastMod != tt.reqLastMod {
					t.Errorf("Expected lastMod = %q, got %q", tt.reqLastMod, lastMod)
				}
				return
			}

			if !tt.expectErr {
				if len(rules) != tt.expectRules {
					t.Errorf("Expected %d rules, got %d", tt.expectRules, len(rules))
				}

				if tt.lastModified != "" && lastMod != tt.lastModified {
					t.Errorf("Expected lastMod = %q, got %q", tt.lastModified, lastMod)
				}
			}
		})
	}
}

func TestLoader_Download_InvalidURL(t *testing.T) {
	loader := NewLoader()
	_, _, err := loader.Download("://invalid-url", "")
	if err == nil {
		t.Error("Expected error for invalid URL")
	}
}

func TestLoader_Download_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	loader := &Loader{
		client: &http.Client{Timeout: 100 * time.Millisecond},
	}

	_, _, err := loader.Download(server.URL, "")
	if err == nil {
		t.Error("Expected timeout error")
	}
}

func TestLoader_Download_WithIfModifiedSince(t *testing.T) {
	lastMod := "Wed, 21 Oct 2015 07:28:00 GMT"
	requestReceived := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		ifModSince := r.Header.Get("If-Modified-Since")
		if ifModSince != lastMod {
			t.Errorf("Expected If-Modified-Since header = %q, got %q", lastMod, ifModSince)
		}
		w.Header().Set("Last-Modified", lastMod)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("||example.com^"))
	}))
	defer server.Close()

	loader := NewLoader()
	_, _, err := loader.Download(server.URL, lastMod)
	if err != nil {
		t.Errorf("Download() error = %v", err)
	}

	if !requestReceived {
		t.Error("Request was not received by test server")
	}
}

func TestLoader_ParseRules_EdgeCases(t *testing.T) {
	loader := NewLoader()

	// Test with very long domain name
	longDomain := "a" + string(make([]byte, 250)) + ".com"
	content := "||" + longDomain + "^"
	rules := loader.ParseRules(content)
	if len(rules) > 0 {
		// Should handle gracefully
		t.Logf("Parsed long domain: %v", rules)
	}

	// Test with special characters
	content = `||example.com^
||test-domain.com^
||sub.domain.example.com^
||123.numeric.com^`
	rules = loader.ParseRules(content)
	if len(rules) != 4 {
		t.Errorf("Expected 4 rules, got %d", len(rules))
	}

	// Test with Unicode (should be handled by normalization)
	content = `||тест.com^`
	rules = loader.ParseRules(content)
	// Should either parse or skip gracefully
	t.Logf("Unicode domain result: %v", rules)
}

func TestLoader_Download_LargeResponse(t *testing.T) {
	// Generate large filter list
	largeContent := ""
	for i := 0; i < 10000; i++ {
		largeContent += "||example" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + ".com^\n"
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(largeContent))
	}))
	defer server.Close()

	loader := NewLoader()
	rules, lastMod, err := loader.Download(server.URL, "")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	if len(rules) != 10000 {
		t.Errorf("Expected 10000 rules, got %d", len(rules))
	}

	if lastMod != "Wed, 21 Oct 2015 07:28:00 GMT" {
		t.Errorf("Expected lastMod to be set, got %q", lastMod)
	}
}

func BenchmarkLoader_ParseRules(b *testing.B) {
	content := ""
	for i := 0; i < 1000; i++ {
		content += "||example" + string(rune('a'+i%26)) + ".com^\n"
	}

	loader := NewLoader()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loader.ParseRules(content)
	}
}

func BenchmarkLoader_ParseRules_WithComments(b *testing.B) {
	content := ""
	for i := 0; i < 1000; i++ {
		if i%10 == 0 {
			content += "! This is a comment\n"
		}
		content += "||example" + string(rune('a'+i%26)) + ".com^\n"
	}

	loader := NewLoader()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loader.ParseRules(content)
	}
}
