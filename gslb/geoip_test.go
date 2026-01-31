package gslb

import (
	"testing"
)

func TestNewGeoIPResolver_InvalidPath(t *testing.T) {
	_, err := NewGeoIPResolver("/nonexistent/path/to/geoip.mmdb")
	if err == nil {
		t.Fatalf("expected error for invalid GeoIP database path")
	}
}

func TestGeoIPResolver_CloseNilReader(t *testing.T) {
	resolver := &GeoIPResolver{reader: nil}
	err := resolver.Close()
	if err != nil {
		t.Fatalf("expected no error when closing nil reader, got: %v", err)
	}
}

func TestGeoIPResolver_CountryNilReader(t *testing.T) {
	resolver := &GeoIPResolver{reader: nil}
	_, _, err := resolver.Country("8.8.8.8")
	if err == nil {
		t.Fatalf("expected error for nil reader")
	}
}

func TestParseIP(t *testing.T) {
	ip := parseIP("8.8.8.8")
	if ip == nil {
		t.Fatalf("expected valid IP")
	}
	if ip.String() != "8.8.8.8" {
		t.Fatalf("expected IP 8.8.8.8, got %s", ip.String())
	}

	invalidIP := parseIP("invalid-ip")
	if invalidIP != nil {
		t.Fatalf("expected nil for invalid IP")
	}
}
