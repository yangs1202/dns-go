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

func TestGeoIPResolver_CloseAlreadyNil(t *testing.T) {
	// Calling Close on a resolver with nil reader multiple times
	resolver := &GeoIPResolver{reader: nil}
	err := resolver.Close()
	if err != nil {
		t.Fatalf("first close should return nil, got: %v", err)
	}
	err = resolver.Close()
	if err != nil {
		t.Fatalf("second close should return nil, got: %v", err)
	}
}

func TestParseIPv6(t *testing.T) {
	ip := parseIP("2001:db8::1")
	if ip == nil {
		t.Fatalf("expected valid IPv6")
	}
	if ip.String() != "2001:db8::1" {
		t.Fatalf("expected 2001:db8::1, got %s", ip.String())
	}
}

func TestGeoIPResolver_CountryNilReaderErrorMessage(t *testing.T) {
	resolver := &GeoIPResolver{reader: nil}
	_, _, err := resolver.Country("1.2.3.4")
	if err == nil {
		t.Fatalf("expected error for nil reader")
	}
	expected := "GeoIP 리더가 초기화되지 않았습니다"
	if err.Error() != expected {
		t.Fatalf("expected error message '%s', got '%s'", expected, err.Error())
	}
}
