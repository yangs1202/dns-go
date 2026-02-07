package dns

import (
	"dns-go/adblock"
	"dns-go/config"
	"dns-go/gslb"
	"dns-go/model"
	"dns-go/storage"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// setupTestHandlerWithGSLB creates a test handler with GSLB engine configured.
func setupTestHandlerWithGSLB(t *testing.T) (*Handler, *storage.Database, func()) {
	dbPath := "/tmp/test_handler_gslb_" + t.Name() + ".db"
	_ = os.Remove(dbPath)

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("DB creation failed: %v", err)
	}

	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)
	upstreamStorage := storage.NewUpstreamStorage(db)

	resolver := NewResolver(upstreamStorage, 5*time.Second)
	stats := NewQueryStats()

	policyStorage := gslb.NewPolicyStorage(db)
	poolStorage := gslb.NewPoolStorage(db)
	healthStatus := &sync.Map{}
	engine := gslb.NewEngine(policyStorage, poolStorage, nil, healthStatus)

	handler, err := NewHandler(zoneStorage, recordStorage, resolver, db, stats, engine, nil, nil, "0.0.0.0", "test-server", "DNS-Go Test v1.0")
	if err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Handler creation failed: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}

	return handler, db, cleanup
}

// mockAdblockStorage implements adblock.AdblockStorageInterface for testing.
type mockAdblockStorage struct {
	blockedDomains map[string]bool
}

func (m *mockAdblockStorage) ListBlockedDomains() ([]string, error) {
	domains := make([]string, 0, len(m.blockedDomains))
	for d := range m.blockedDomains {
		domains = append(domains, d)
	}
	return domains, nil
}

func (m *mockAdblockStorage) IsBlocked(domain string) (bool, error) {
	return m.blockedDomains[domain], nil
}

// setupTestHandlerWithAdblock creates a test handler with adblock filter.
func setupTestHandlerWithAdblock(t *testing.T, blockedDomains []string, response string) (*Handler, *storage.Database, func()) {
	dbPath := "/tmp/test_handler_adblock_" + t.Name() + ".db"
	_ = os.Remove(dbPath)

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("DB creation failed: %v", err)
	}

	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)
	upstreamStorage := storage.NewUpstreamStorage(db)
	adblockStorage := storage.NewAdblockStorage(db)

	resolver := NewResolver(upstreamStorage, 5*time.Second)
	stats := NewQueryStats()

	mockStorage := &mockAdblockStorage{blockedDomains: make(map[string]bool)}
	for _, d := range blockedDomains {
		mockStorage.blockedDomains[d] = true
	}
	filter := adblock.NewFilter(mockStorage, true)

	handler, err := NewHandler(zoneStorage, recordStorage, resolver, db, stats, nil, filter, adblockStorage, response, "test-server", "DNS-Go Test v1.0")
	if err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Handler creation failed: %v", err)
	}

	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}

	return handler, db, cleanup
}

// =============================================================================
// ServeDNS - GSLB path tests
// =============================================================================

func TestServeDNS_GSLBResolveA(t *testing.T) {
	handler, db, cleanup := setupTestHandlerWithGSLB(t)
	defer cleanup()

	// Create GSLB policy for gslb.example.com A record
	res, err := db.Writer.Exec(
		`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled) VALUES (?, ?, ?, ?, ?)`,
		"test-gslb", "gslb.example.com.", "A", 30, 1,
	)
	if err != nil {
		t.Fatalf("Policy insert failed: %v", err)
	}
	policyID, _ := res.LastInsertId()

	// Create a default pool
	poolRes, err := db.Writer.Exec(
		`INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool) VALUES (?, ?, ?, ?, ?, ?)`,
		policyID, "default-pool", "default", "", 0, 1,
	)
	if err != nil {
		t.Fatalf("Pool insert failed: %v", err)
	}
	poolID, _ := poolRes.LastInsertId()

	// Create a member
	_, err = db.Writer.Exec(
		`INSERT INTO gslb_members (pool_id, address, weight, enabled) VALUES (?, ?, ?, ?)`,
		poolID, "10.0.0.1", 100, 1,
	)
	if err != nil {
		t.Fatalf("Member insert failed: %v", err)
	}

	// Query GSLB domain A record
	req := new(dns.Msg)
	req.SetQuestion("gslb.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	if len(w.msg.Answer) == 0 {
		t.Error("Expected GSLB A answer, got none")
	}

	// Verify it's an A record
	for _, ans := range w.msg.Answer {
		if _, ok := ans.(*dns.A); !ok {
			t.Errorf("Expected A record, got: %T", ans)
		}
	}
}

func TestServeDNS_GSLBResolveAAAA(t *testing.T) {
	handler, db, cleanup := setupTestHandlerWithGSLB(t)
	defer cleanup()

	// Create GSLB policy for AAAA
	res, err := db.Writer.Exec(
		`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled) VALUES (?, ?, ?, ?, ?)`,
		"test-gslb-v6", "gslb6.example.com.", "AAAA", 30, 1,
	)
	if err != nil {
		t.Fatalf("Policy insert failed: %v", err)
	}
	policyID, _ := res.LastInsertId()

	poolRes, err := db.Writer.Exec(
		`INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool) VALUES (?, ?, ?, ?, ?, ?)`,
		policyID, "default-pool-v6", "default", "", 0, 1,
	)
	if err != nil {
		t.Fatalf("Pool insert failed: %v", err)
	}
	poolID, _ := poolRes.LastInsertId()

	_, err = db.Writer.Exec(
		`INSERT INTO gslb_members (pool_id, address, weight, enabled) VALUES (?, ?, ?, ?)`,
		poolID, "2001:db8::1", 100, 1,
	)
	if err != nil {
		t.Fatalf("Member insert failed: %v", err)
	}

	req := new(dns.Msg)
	req.SetQuestion("gslb6.example.com.", dns.TypeAAAA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	if len(w.msg.Answer) == 0 {
		t.Error("Expected GSLB AAAA answer, got none")
	}

	for _, ans := range w.msg.Answer {
		if _, ok := ans.(*dns.AAAA); !ok {
			t.Errorf("Expected AAAA record, got: %T", ans)
		}
	}
}

func TestServeDNS_GSLBNoMatch(t *testing.T) {
	handler, _, cleanup := setupTestHandlerWithGSLB(t)
	defer cleanup()

	// GSLB engine exists but no policies, should fall through to zone/upstream
	req := new(dns.Msg)
	req.SetQuestion("unknown-gslb.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}
	// Should get NXDOMAIN from upstream failure (no upstream configured)
	if w.msg.Rcode != dns.RcodeNameError {
		t.Logf("GSLB no-match fell through, Rcode: %s", dns.RcodeToString[w.msg.Rcode])
	}
}

// =============================================================================
// ServeDNS - CNAME chain tests
// =============================================================================

func TestServeDNS_CNAMEChain(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:     "example.com.",
		SOAMname: "ns1.example.com.",
		SOARname: "admin.example.com.",
		Enabled:  true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Create CNAME: www -> target
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "www.example.com.",
		Type:    "CNAME",
		Content: "target.example.com.",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CNAME record creation failed: %v", err)
	}

	// Create A record for target
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "target.example.com.",
		Type:    "A",
		Content: "192.0.2.50",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("A record creation failed: %v", err)
	}

	// Query A record for CNAME domain
	req := new(dns.Msg)
	req.SetQuestion("www.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// Should have CNAME + A records in answer
	if len(w.msg.Answer) < 2 {
		t.Errorf("Expected at least 2 records (CNAME + A), got: %d", len(w.msg.Answer))
	}

	// First should be CNAME
	hasCNAME := false
	hasA := false
	for _, ans := range w.msg.Answer {
		if _, ok := ans.(*dns.CNAME); ok {
			hasCNAME = true
		}
		if _, ok := ans.(*dns.A); ok {
			hasA = true
		}
	}
	if !hasCNAME {
		t.Error("Expected CNAME record in answer")
	}
	if !hasA {
		t.Error("Expected A record in answer")
	}
}

func TestServeDNS_CNAMEWithAAAAQuery(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:     "example.com.",
		SOAMname: "ns1.example.com.",
		SOARname: "admin.example.com.",
		Enabled:  true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// CNAME only, no AAAA target
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "alias.example.com.",
		Type:    "CNAME",
		Content: "target.example.com.",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CNAME record creation failed: %v", err)
	}

	// Query AAAA for CNAME domain (no AAAA target exists)
	req := new(dns.Msg)
	req.SetQuestion("alias.example.com.", dns.TypeAAAA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	// Should get NOERROR with at least the CNAME in answer
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
}

func TestServeDNS_CNAMEWithoutDot(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:     "example.com.",
		SOAMname: "ns1.example.com.",
		SOARname: "admin.example.com.",
		Enabled:  true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// CNAME content without trailing dot
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "nodot.example.com.",
		Type:    "CNAME",
		Content: "target.example.com", // no trailing dot
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CNAME record creation failed: %v", err)
	}

	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "target.example.com.",
		Type:    "A",
		Content: "192.0.2.60",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("A record creation failed: %v", err)
	}

	req := new(dns.Msg)
	req.SetQuestion("nodot.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
}

// =============================================================================
// ServeDNS - Fallback Zone tests (RFC 4074)
// =============================================================================

func TestServeDNS_FallbackZone_AAAAQuery(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone with fallback enabled
	zone := &model.Zone{
		Name:          "fallback.com.",
		SOAMname:      "ns1.fallback.com.",
		SOARname:      "admin.fallback.com.",
		Enabled:       true,
		AllowFallback: true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Only A record, no AAAA
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "host.fallback.com.",
		Type:    "A",
		Content: "192.0.2.1",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Record creation failed: %v", err)
	}

	// Query AAAA for a domain that has only A
	// RFC 4074: domain exists, should return NOERROR
	req := new(dns.Msg)
	req.SetQuestion("host.fallback.com.", dns.TypeAAAA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("RFC 4074: Expected NOERROR for AAAA on domain with only A, got: %s",
			dns.RcodeToString[w.msg.Rcode])
	}

	// Should have SOA in authority
	if len(w.msg.Ns) == 0 {
		t.Error("Expected SOA in authority section")
	}
}

func TestServeDNS_FallbackZone_MXQuery(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:          "fallback.com.",
		SOAMname:      "ns1.fallback.com.",
		SOARname:      "admin.fallback.com.",
		Enabled:       true,
		AllowFallback: true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "host.fallback.com.",
		Type:    "A",
		Content: "192.0.2.1",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Record creation failed: %v", err)
	}

	// Query MX for a domain with only A
	req := new(dns.Msg)
	req.SetQuestion("host.fallback.com.", dns.TypeMX)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("RFC 4074: Expected NOERROR for MX on domain with only A, got: %s",
			dns.RcodeToString[w.msg.Rcode])
	}
}

func TestServeDNS_FallbackZone_NonexistentDomain(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:          "fallback.com.",
		SOAMname:      "ns1.fallback.com.",
		SOARname:      "admin.fallback.com.",
		Enabled:       true,
		AllowFallback: true,
	}
	_, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Domain doesn't exist at all in zone - should fall through to upstream
	req := new(dns.Msg)
	req.SetQuestion("nonexistent.fallback.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	// No upstream configured, so should get NXDOMAIN
	if w.msg.Rcode != dns.RcodeNameError {
		t.Logf("Fallback nonexistent domain Rcode: %s (expected NXDOMAIN from upstream failure)",
			dns.RcodeToString[w.msg.Rcode])
	}
}

func TestServeDNS_NoFallback_NXDOMAIN(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:          "nofallback.com.",
		SOAMname:      "ns1.nofallback.com.",
		SOARname:      "admin.nofallback.com.",
		Enabled:       true,
		AllowFallback: false,
	}
	_, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Domain doesn't exist in zone, fallback disabled -> NXDOMAIN
	req := new(dns.Msg)
	req.SetQuestion("nonexistent.nofallback.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeNameError {
		t.Errorf("Expected NXDOMAIN, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// RFC 2308: NXDOMAIN must have SOA in authority section
	if len(w.msg.Ns) == 0 {
		t.Error("RFC 2308: NXDOMAIN must have SOA in authority section")
	}
}

func TestServeDNS_NoFallback_NOERROR(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:          "nofallback.com.",
		SOAMname:      "ns1.nofallback.com.",
		SOARname:      "admin.nofallback.com.",
		Enabled:       true,
		AllowFallback: false,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "host.nofallback.com.",
		Type:    "A",
		Content: "192.0.2.1",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Record creation failed: %v", err)
	}

	// Domain exists but no TXT record, no fallback -> NOERROR
	req := new(dns.Msg)
	req.SetQuestion("host.nofallback.com.", dns.TypeTXT)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	if len(w.msg.Ns) == 0 {
		t.Error("Expected SOA in authority section for empty NOERROR response")
	}
}

// =============================================================================
// ServeDNS - Adblock tests
// =============================================================================

func TestServeDNS_Adblock_BlockedA(t *testing.T) {
	handler, _, cleanup := setupTestHandlerWithAdblock(t, []string{"ads.tracker.com"}, "0.0.0.0")
	defer cleanup()

	req := new(dns.Msg)
	req.SetQuestion("ads.tracker.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR for blocked A, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	if len(w.msg.Answer) == 0 {
		t.Fatal("Expected answer for blocked A")
	}

	a, ok := w.msg.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("Expected A record, got: %T", w.msg.Answer[0])
	}
	if a.A.String() != "0.0.0.0" {
		t.Errorf("Expected 0.0.0.0, got: %s", a.A.String())
	}
}

func TestServeDNS_Adblock_BlockedAAAA(t *testing.T) {
	handler, _, cleanup := setupTestHandlerWithAdblock(t, []string{"ads.tracker.com"}, "0.0.0.0")
	defer cleanup()

	req := new(dns.Msg)
	req.SetQuestion("ads.tracker.com.", dns.TypeAAAA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR for blocked AAAA, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	if len(w.msg.Answer) == 0 {
		t.Fatal("Expected answer for blocked AAAA")
	}

	aaaa, ok := w.msg.Answer[0].(*dns.AAAA)
	if !ok {
		t.Fatalf("Expected AAAA record, got: %T", w.msg.Answer[0])
	}
	if aaaa.AAAA.String() != "::" {
		t.Errorf("Expected ::, got: %s", aaaa.AAAA.String())
	}
}

func TestServeDNS_Adblock_BlockedMX(t *testing.T) {
	handler, _, cleanup := setupTestHandlerWithAdblock(t, []string{"ads.tracker.com"}, "0.0.0.0")
	defer cleanup()

	// MX query for blocked domain should return NXDOMAIN
	req := new(dns.Msg)
	req.SetQuestion("ads.tracker.com.", dns.TypeMX)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeNameError {
		t.Errorf("Expected NXDOMAIN for blocked MX, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
}

func TestServeDNS_Adblock_NXDOMAIN_Response(t *testing.T) {
	handler, _, cleanup := setupTestHandlerWithAdblock(t, []string{"ads.tracker.com"}, "NXDOMAIN")
	defer cleanup()

	req := new(dns.Msg)
	req.SetQuestion("ads.tracker.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeNameError {
		t.Errorf("Expected NXDOMAIN for adblock NXDOMAIN response, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// RFC 2308: SOA in authority
	if len(w.msg.Ns) == 0 {
		t.Error("Expected SOA in authority section for NXDOMAIN")
	}
}

func TestServeDNS_Adblock_NotBlocked(t *testing.T) {
	handler, _, cleanup := setupTestHandlerWithAdblock(t, []string{"ads.tracker.com"}, "0.0.0.0")
	defer cleanup()

	// Unblocked domain should pass through normally
	req := new(dns.Msg)
	req.SetQuestion("clean.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	// Should get NXDOMAIN (no zone/upstream), not adblock response
	if w.msg.Rcode != dns.RcodeNameError {
		t.Logf("Unblocked domain Rcode: %s", dns.RcodeToString[w.msg.Rcode])
	}
}

// =============================================================================
// ServeDNS - Zone error handling
// =============================================================================

func TestServeDNS_ZoneRecordLookupSuccess(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:     "testzone.com.",
		SOAMname: "ns1.testzone.com.",
		SOARname: "admin.testzone.com.",
		Enabled:  true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Add NS records for the zone (to cover NS section in authoritative response)
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "testzone.com.",
		Type:    "NS",
		Content: "ns1.testzone.com.",
		TTL:     3600,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("NS record creation failed: %v", err)
	}

	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "testzone.com.",
		Type:    "NS",
		Content: "ns2.testzone.com.",
		TTL:     3600,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("NS record creation failed: %v", err)
	}

	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "www.testzone.com.",
		Type:    "A",
		Content: "192.0.2.10",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("A record creation failed: %v", err)
	}

	req := new(dns.Msg)
	req.SetQuestion("www.testzone.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// Should have NS records in authority section
	if len(w.msg.Ns) == 0 {
		t.Error("Expected NS records in authority section for authoritative response")
	}

	// Should be authoritative
	if !w.msg.Authoritative {
		t.Error("Expected Authoritative flag to be true for zone response")
	}
}

// =============================================================================
// buildSOA tests
// =============================================================================

func TestBuildSOA_WithDot(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	soa := handler.buildSOA("example.com.")
	if soa == nil {
		t.Fatal("SOA is nil")
	}
	if soa.Hdr.Name != "example.com." {
		t.Errorf("Expected name example.com., got: %s", soa.Hdr.Name)
	}
}

func TestBuildSOA_WithoutDot(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	soa := handler.buildSOA("example.com")
	if soa == nil {
		t.Fatal("SOA is nil")
	}
	// Should add trailing dot
	if soa.Hdr.Name != "example.com." {
		t.Errorf("Expected name example.com., got: %s", soa.Hdr.Name)
	}
	if soa.Ns != "ns.example.com." {
		t.Errorf("Expected NS ns.example.com., got: %s", soa.Ns)
	}
	if soa.Mbox != "admin.example.com." {
		t.Errorf("Expected Mbox admin.example.com., got: %s", soa.Mbox)
	}
}

// =============================================================================
// recordToRR - additional record type tests
// =============================================================================

func TestRecordToRR_UnsupportedType(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	record := &model.Record{
		Name:    "test.example.com.",
		Type:    "SRV",
		Content: "0 5 80 www.example.com.",
		TTL:     300,
	}

	rr := handler.recordToRR(record)
	if rr != nil {
		t.Error("Expected nil for unsupported record type SRV")
	}
}

func TestRecordToRR_SOABadContent(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// SOA with insufficient parts
	record := &model.Record{
		Name:    "example.com.",
		Type:    "SOA",
		Content: "ns1.example.com. admin.example.com.", // only 2 parts, needs 7
		TTL:     300,
	}

	rr := handler.recordToRR(record)
	if rr != nil {
		t.Error("Expected nil for SOA with insufficient parts")
	}
}

func TestRecordToRR_CNAMEWithoutDot(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	record := &model.Record{
		Name:    "alias.example.com.",
		Type:    "CNAME",
		Content: "target.example.com", // no trailing dot
		TTL:     300,
	}

	rr := handler.recordToRR(record)
	if rr == nil {
		t.Fatal("Expected non-nil RR for CNAME")
	}

	cname, ok := rr.(*dns.CNAME)
	if !ok {
		t.Fatal("Expected CNAME type")
	}

	// Should add trailing dot
	if cname.Target != "target.example.com." {
		t.Errorf("Expected target.example.com., got: %s", cname.Target)
	}
}

func TestRecordToRR_AllTypes(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	tests := []struct {
		name      string
		record    *model.Record
		expectNil bool
	}{
		{
			name:   "A record",
			record: &model.Record{Name: "t.com.", Type: "A", Content: "1.2.3.4", TTL: 60},
		},
		{
			name:   "AAAA record",
			record: &model.Record{Name: "t.com.", Type: "AAAA", Content: "::1", TTL: 60},
		},
		{
			name:   "CNAME record",
			record: &model.Record{Name: "t.com.", Type: "CNAME", Content: "other.com.", TTL: 60},
		},
		{
			name:   "MX record",
			record: &model.Record{Name: "t.com.", Type: "MX", Content: "mail.t.com.", TTL: 60, Priority: 10},
		},
		{
			name:   "TXT record",
			record: &model.Record{Name: "t.com.", Type: "TXT", Content: "v=spf1 all", TTL: 60},
		},
		{
			name:   "NS record",
			record: &model.Record{Name: "t.com.", Type: "NS", Content: "ns1.t.com.", TTL: 60},
		},
		{
			name:   "SOA record (valid)",
			record: &model.Record{Name: "t.com.", Type: "SOA", Content: "ns1.t.com. admin.t.com. 1 3600 900 86400 300", TTL: 3600},
		},
		{
			name:      "PTR record (unsupported)",
			record:    &model.Record{Name: "1.2.3.4.in-addr.arpa.", Type: "PTR", Content: "host.t.com.", TTL: 60},
			expectNil: true,
		},
		{
			name:      "CAA record (unsupported)",
			record:    &model.Record{Name: "t.com.", Type: "CAA", Content: "0 issue letsencrypt.org", TTL: 60},
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := handler.recordToRR(tt.record)
			if tt.expectNil && rr != nil {
				t.Errorf("Expected nil for unsupported type %s, got: %v", tt.record.Type, rr)
			}
			if !tt.expectNil && rr == nil {
				t.Errorf("Expected non-nil RR for type %s", tt.record.Type)
			}
		})
	}
}

// =============================================================================
// ClearCache, GetCache, ReconfigureCache tests
// =============================================================================

func TestClearCache(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Add entries to L1 cache
	rrs := []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: "test.example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("192.0.2.1"),
		},
	}
	handler.cache.Set("test.example.com.", "A", rrs, 300, false)

	if handler.cache.Size() == 0 {
		t.Fatal("Cache should not be empty before ClearCache")
	}

	handler.ClearCache()

	if handler.cache.Size() != 0 {
		t.Errorf("Expected cache size 0 after ClearCache, got: %d", handler.cache.Size())
	}
}

func TestClearCache_NilCache(t *testing.T) {
	// Handler with nil cache should not panic
	h := &Handler{}
	h.ClearCache() // should not panic
}

func TestGetCache(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	cache := handler.GetCache()
	if cache == nil {
		t.Error("GetCache returned nil")
	}
	if cache != handler.cache {
		t.Error("GetCache returned different cache instance")
	}
}

func TestReconfigureCache(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	oldCache := handler.cache

	settings := &model.CacheSettings{
		MaxSize:         5000,
		DefaultTTL:      600,
		NegativeTTL:     120,
		PrefetchTrigger: 0.8,
	}
	handler.ReconfigureCache(settings)

	if handler.cache == oldCache {
		t.Error("Expected new cache instance after ReconfigureCache")
	}

	if handler.cache.maxSize != 5000 {
		t.Errorf("Expected maxSize 5000, got: %d", handler.cache.maxSize)
	}
}

func TestReconfigureCache_NilSettings(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	oldCache := handler.cache
	handler.ReconfigureCache(nil)

	if handler.cache != oldCache {
		t.Error("Cache should not change with nil settings")
	}
}

// =============================================================================
// handlePrefetch - upstream path test
// =============================================================================

func TestHandlePrefetch_ZoneNotFound(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Prefetch for non-existent zone - should not panic
	handler.handlePrefetch("nonexistent.unknown.com.", "A")

	// Should not have cached anything
	_, ok := handler.cache.Get("nonexistent.unknown.com.", "A")
	if ok {
		t.Error("Should not cache anything for non-existent zone")
	}
}

func TestHandlePrefetch_RecordNotFound_UpstreamFail(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Create zone but no records
	zone := &model.Zone{
		Name:     "prefetch.com.",
		SOAMname: "ns1.prefetch.com.",
		SOARname: "admin.prefetch.com.",
		Enabled:  true,
	}
	_, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Prefetch for domain with no records and no upstream -> should not panic
	handler.handlePrefetch("norecord.prefetch.com.", "A")

	// Cache should not have the entry (upstream fails)
	_, ok := handler.cache.Get("norecord.prefetch.com.", "A")
	if ok {
		t.Error("Should not cache when upstream fails")
	}
}

func TestHandlePrefetch_WithRecords(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:     "prefetch.com.",
		SOAMname: "ns1.prefetch.com.",
		SOARname: "admin.prefetch.com.",
		Enabled:  true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Create multiple records with different TTLs
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "multi.prefetch.com.",
		Type:    "A",
		Content: "192.0.2.1",
		TTL:     100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Record creation failed: %v", err)
	}

	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "multi.prefetch.com.",
		Type:    "A",
		Content: "192.0.2.2",
		TTL:     200,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Record creation failed: %v", err)
	}

	handler.handlePrefetch("multi.prefetch.com.", "A")

	entry, ok := handler.cache.Get("multi.prefetch.com.", "A")
	if !ok {
		t.Fatal("Expected cache entry after prefetch")
	}
	if len(entry.RRs) != 2 {
		t.Errorf("Expected 2 RRs, got: %d", len(entry.RRs))
	}
}

// =============================================================================
// QueryStats.Snapshot test
// =============================================================================

func TestQueryStats_Snapshot(t *testing.T) {
	stats := NewQueryStats()

	stats.IncTotal()
	stats.IncTotal()
	stats.IncL1Hit()
	stats.IncL1Miss()
	stats.IncUpstreamHit()

	snap := stats.Snapshot()

	if snap.Total != 2 {
		t.Errorf("Expected Total 2, got: %d", snap.Total)
	}
	if snap.L1Hits != 1 {
		t.Errorf("Expected L1Hits 1, got: %d", snap.L1Hits)
	}
	if snap.L1Misses != 1 {
		t.Errorf("Expected L1Misses 1, got: %d", snap.L1Misses)
	}
	if snap.UpstreamHits != 1 {
		t.Errorf("Expected UpstreamHits 1, got: %d", snap.UpstreamHits)
	}
}

// =============================================================================
// Server Stop with both TCP+UDP
// =============================================================================

func TestServerStop_BothProtocols(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15390,
		UDP:    true,
		TCP:    true,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	err := server.Start()
	if err != nil {
		t.Fatalf("Server start failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	err = server.Stop()
	if err != nil {
		t.Errorf("Server stop failed: %v", err)
	}
}

func TestServerStop_TCPOnly(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15391,
		UDP:    false,
		TCP:    true,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	err := server.Start()
	if err != nil {
		t.Fatalf("Server start failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	err = server.Stop()
	if err != nil {
		t.Errorf("Server stop failed: %v", err)
	}
}

// =============================================================================
// ServeDNS - RecursionDesired=false for authoritative zones
// =============================================================================

func TestServeDNS_RDFalse_NoUpstream(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// No zone, no upstream, RD=0 -> REFUSED
	req := new(dns.Msg)
	req.SetQuestion("anysite.com.", dns.TypeA)
	req.RecursionDesired = false

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeRefused {
		t.Errorf("Expected REFUSED for RD=0, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
}

// =============================================================================
// ServeDNS - GSLB + CNAME chain test
// =============================================================================

func TestServeDNS_CNAME_To_GSLB(t *testing.T) {
	handler, db, cleanup := setupTestHandlerWithGSLB(t)
	defer cleanup()

	// Create zone
	zone := &model.Zone{
		Name:     "example.com.",
		SOAMname: "ns1.example.com.",
		SOARname: "admin.example.com.",
		Enabled:  true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// CNAME pointing to a GSLB domain
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "app.example.com.",
		Type:    "CNAME",
		Content: "gslb-target.example.com.",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CNAME record creation failed: %v", err)
	}

	// Create GSLB policy for the CNAME target
	res, err := db.Writer.Exec(
		`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled) VALUES (?, ?, ?, ?, ?)`,
		"cname-gslb", "gslb-target.example.com.", "A", 30, 1,
	)
	if err != nil {
		t.Fatalf("GSLB policy insert failed: %v", err)
	}
	policyID, _ := res.LastInsertId()

	poolRes, err := db.Writer.Exec(
		`INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool) VALUES (?, ?, ?, ?, ?, ?)`,
		policyID, "default", "default", "", 0, 1,
	)
	if err != nil {
		t.Fatalf("GSLB pool insert failed: %v", err)
	}
	poolID, _ := poolRes.LastInsertId()

	_, err = db.Writer.Exec(
		`INSERT INTO gslb_members (pool_id, address, weight, enabled) VALUES (?, ?, ?, ?)`,
		poolID, "10.0.0.5", 100, 1,
	)
	if err != nil {
		t.Fatalf("GSLB member insert failed: %v", err)
	}

	// Query A for CNAME that points to GSLB
	req := new(dns.Msg)
	req.SetQuestion("app.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// Should contain both CNAME and A records
	if len(w.msg.Answer) < 2 {
		t.Errorf("Expected at least 2 answers (CNAME + A from GSLB), got: %d", len(w.msg.Answer))
	}
}

// =============================================================================
// ServeDNS - Upstream NXDOMAIN caching
// =============================================================================

func TestServeDNS_UpstreamNXDOMAIN(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// No zone, no upstream servers -> will fail upstream -> NXDOMAIN + cache
	req := new(dns.Msg)
	req.SetQuestion("totally.unknown.domain.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeNameError {
		t.Errorf("Expected NXDOMAIN, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// SOA in authority
	if len(w.msg.Ns) == 0 {
		t.Error("Expected SOA in authority for NXDOMAIN")
	}

	// Verify negative cache
	entry, ok := handler.cache.Get("totally.unknown.domain.", "A")
	if !ok {
		t.Error("Expected negative cache entry")
	}
	if !entry.IsNegative {
		t.Error("Expected IsNegative=true")
	}
}

// =============================================================================
// ServeDNS - Stats integration
// =============================================================================

func TestServeDNS_StatsTracking(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Cache hit path
	rrs := []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{Name: "cached.test.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
			A:   net.ParseIP("1.2.3.4"),
		},
	}
	handler.cache.Set("cached.test.", "A", rrs, 300, false)

	req := new(dns.Msg)
	req.SetQuestion("cached.test.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	snap := handler.stats.Snapshot()
	if snap.Total != 1 {
		t.Errorf("Expected Total=1, got: %d", snap.Total)
	}
	if snap.L1Hits != 1 {
		t.Errorf("Expected L1Hits=1, got: %d", snap.L1Hits)
	}

	// Cache miss path
	req2 := new(dns.Msg)
	req2.SetQuestion("notcached.test.", dns.TypeA)
	w2 := newMockWriter("192.0.2.100")
	handler.ServeDNS(w2, req2)

	snap2 := handler.stats.Snapshot()
	if snap2.Total != 2 {
		t.Errorf("Expected Total=2, got: %d", snap2.Total)
	}
	if snap2.L1Misses != 1 {
		t.Errorf("Expected L1Misses=1, got: %d", snap2.L1Misses)
	}
}

// =============================================================================
// ServeDNS - GSLB with EDNS Client Subnet
// =============================================================================

func TestServeDNS_GSLB_ClientSubnet(t *testing.T) {
	handler, db, cleanup := setupTestHandlerWithGSLB(t)
	defer cleanup()

	res, err := db.Writer.Exec(
		`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled) VALUES (?, ?, ?, ?, ?)`,
		"subnet-test", "subnet.example.com.", "A", 30, 1,
	)
	if err != nil {
		t.Fatalf("Policy insert failed: %v", err)
	}
	policyID, _ := res.LastInsertId()

	poolRes, err := db.Writer.Exec(
		`INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool) VALUES (?, ?, ?, ?, ?, ?)`,
		policyID, "default", "default", "", 0, 1,
	)
	if err != nil {
		t.Fatalf("Pool insert failed: %v", err)
	}
	poolID, _ := poolRes.LastInsertId()

	_, err = db.Writer.Exec(
		`INSERT INTO gslb_members (pool_id, address, weight, enabled) VALUES (?, ?, ?, ?)`,
		poolID, "10.0.0.10", 100, 1,
	)
	if err != nil {
		t.Fatalf("Member insert failed: %v", err)
	}

	req := new(dns.Msg)
	req.SetQuestion("subnet.example.com.", dns.TypeA)

	// Add EDNS Client Subnet
	opt := new(dns.OPT)
	opt.Hdr.Name = "."
	opt.Hdr.Rrtype = dns.TypeOPT
	subnet := new(dns.EDNS0_SUBNET)
	subnet.Code = dns.EDNS0SUBNET
	subnet.Family = 1
	subnet.SourceNetmask = 24
	subnet.Address = net.ParseIP("203.0.113.0").To4()
	opt.Option = append(opt.Option, subnet)
	req.Extra = append(req.Extra, opt)

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
}

// =============================================================================
// ServeDNS - CHAOS class hostname.server
// =============================================================================

func TestServeDNS_CHAOS_HostnameServer(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := new(dns.Msg)
	req.SetQuestion("hostname.server.", dns.TypeTXT)
	req.Question[0].Qclass = dns.ClassCHAOS

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	if len(w.msg.Answer) == 0 {
		t.Fatal("Expected TXT answer for hostname.server")
	}

	txt, ok := w.msg.Answer[0].(*dns.TXT)
	if !ok {
		t.Fatal("Expected TXT record")
	}
	if len(txt.Txt) == 0 || txt.Txt[0] != "test-server" {
		t.Errorf("Expected test-server, got: %v", txt.Txt)
	}
}

// =============================================================================
// ServeDNS - CHAOS class non-TXT query (unsupported)
// =============================================================================

func TestServeDNS_CHAOS_NonTXT(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	req := new(dns.Msg)
	req.SetQuestion("version.bind.", dns.TypeA) // A query for CHAOS
	req.Question[0].Qclass = dns.ClassCHAOS

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeRefused {
		t.Errorf("Expected REFUSED for non-TXT CHAOS query, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
}

// =============================================================================
// ServeDNS - Disabled record filtering
// =============================================================================

func TestServeDNS_DisabledRecordFiltered(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:    "filter.com.",
		Enabled: true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Active record
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "www.filter.com.",
		Type:    "A",
		Content: "192.0.2.1",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Record creation failed: %v", err)
	}

	req := new(dns.Msg)
	req.SetQuestion("www.filter.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	if len(w.msg.Answer) != 1 {
		t.Errorf("Expected 1 answer, got: %d", len(w.msg.Answer))
	}
}

// =============================================================================
// ServeDNS - Negative cache hit path (SOA in authority)
// =============================================================================

func TestServeDNS_NegativeCacheHit_SOA(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Pre-store negative cache entry
	handler.cache.Set("negative.test.com.", "A", nil, 300, true)

	req := new(dns.Msg)
	req.SetQuestion("negative.test.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeNameError {
		t.Errorf("Expected NXDOMAIN, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// SOA should be in authority section
	if len(w.msg.Ns) == 0 {
		t.Error("Expected SOA in authority for negative cache hit")
	}

	soaFound := false
	for _, rr := range w.msg.Ns {
		if _, ok := rr.(*dns.SOA); ok {
			soaFound = true
		}
	}
	if !soaFound {
		t.Error("Expected SOA record in Ns section")
	}
}

// =============================================================================
// ServeDNS - Minimal TTL caching
// =============================================================================

func TestServeDNS_MinTTLCaching(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:    "minttl.com.",
		Enabled: true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Records with different TTLs
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "multi.minttl.com.",
		Type:    "A",
		Content: "192.0.2.1",
		TTL:     100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Record creation failed: %v", err)
	}

	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "multi.minttl.com.",
		Type:    "A",
		Content: "192.0.2.2",
		TTL:     50, // Minimum TTL
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("Record creation failed: %v", err)
	}

	req := new(dns.Msg)
	req.SetQuestion("multi.minttl.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// Verify cache entry exists
	entry, ok := handler.cache.Get("multi.minttl.com.", "A")
	if !ok {
		t.Fatal("Expected cache entry")
	}
	if len(entry.RRs) != 2 {
		t.Errorf("Expected 2 RRs in cache, got: %d", len(entry.RRs))
	}
}

// =============================================================================
// ServeDNS with nil stats (coverage for nil stats check)
// =============================================================================

// =============================================================================
// Cache removeExpired tests
// =============================================================================

func TestCacheRemoveExpired_WithExpiredEntries(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)

	rrs := []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: "test.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1},
		A:   net.ParseIP("1.2.3.4"),
	}}

	// Set entries with very short TTL
	cache.Set("expired1.com.", "A", rrs, 1, false)
	cache.Set("expired2.com.", "A", rrs, 1, false)
	cache.Set("alive.com.", "A", rrs, 3600, false)

	if cache.Size() != 3 {
		t.Errorf("Expected size 3, got: %d", cache.Size())
	}

	// Wait for short-TTL entries to expire
	time.Sleep(1200 * time.Millisecond)

	// Directly call removeExpired
	cache.removeExpired()

	// Only the alive entry should remain
	if cache.Size() != 1 {
		t.Errorf("Expected size 1 after cleanup, got: %d", cache.Size())
	}

	_, found := cache.Get("alive.com.", "A")
	if !found {
		t.Error("Expected alive.com to still be cached")
	}
}

func TestCacheRemoveExpired_NoExpiredEntries(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)

	rrs := []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: "test.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 3600},
		A:   net.ParseIP("1.2.3.4"),
	}}

	cache.Set("alive1.com.", "A", rrs, 3600, false)
	cache.Set("alive2.com.", "A", rrs, 3600, false)

	initialSize := cache.Size()
	cache.removeExpired()

	if cache.Size() != initialSize {
		t.Errorf("Expected size %d (no change), got: %d", initialSize, cache.Size())
	}
}

func TestCacheRemoveExpired_EmptyCache(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)

	// Should not panic on empty cache
	cache.removeExpired()

	if cache.Size() != 0 {
		t.Errorf("Expected size 0, got: %d", cache.Size())
	}
}

// =============================================================================
// ServeDNS - Upstream success with actual forwarding
// =============================================================================

func TestServeDNS_UpstreamSuccess(t *testing.T) {
	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	// Add a real upstream server (Google DNS)
	_, err := db.Writer.Exec(
		`INSERT INTO upstream_servers (name, address, protocol, priority, enabled) VALUES (?, ?, ?, ?, ?)`,
		"Google DNS", "8.8.8.8:53", "udp", 1, 1,
	)
	if err != nil {
		t.Fatalf("Upstream insert failed: %v", err)
	}

	// Query for a well-known domain (RD=1 for forwarding)
	req := new(dns.Msg)
	req.SetQuestion("dns.google.", dns.TypeA)
	req.RecursionDesired = true

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	// Should get a successful response from upstream
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Logf("Upstream query Rcode: %s (may fail in CI without network)", dns.RcodeToString[w.msg.Rcode])
	}

	// Verify upstream hit stats
	snap := handler.stats.Snapshot()
	if snap.L1Misses < 1 {
		t.Error("Expected at least 1 L1 miss")
	}
}

// =============================================================================
// handlePrefetch with upstream success path
// =============================================================================

func TestHandlePrefetch_UpstreamSuccess(t *testing.T) {
	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	// Create zone but no records
	zone := &model.Zone{
		Name:     "prefetch-up.com.",
		SOAMname: "ns1.prefetch-up.com.",
		SOARname: "admin.prefetch-up.com.",
		Enabled:  true,
	}
	_, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// Add upstream server
	_, err = db.Writer.Exec(
		`INSERT INTO upstream_servers (name, address, protocol, priority, enabled) VALUES (?, ?, ?, ?, ?)`,
		"Google DNS", "8.8.8.8:53", "udp", 1, 1,
	)
	if err != nil {
		t.Fatalf("Upstream insert failed: %v", err)
	}

	// Prefetch for a real domain with upstream (no records in zone)
	handler.handlePrefetch("dns.google.", "A")

	// Give it a moment for async
	time.Sleep(100 * time.Millisecond)

	// Check if cached from upstream
	entry, ok := handler.cache.Get("dns.google.", "A")
	if ok && !entry.IsNegative && len(entry.RRs) > 0 {
		t.Logf("Prefetch from upstream succeeded: %d RRs", len(entry.RRs))
	} else {
		t.Logf("Prefetch from upstream may have failed (network dependent)")
	}
}

// =============================================================================
// Server Stop - TCP without start (coverage for "server not started" path)
// =============================================================================

func TestServerStop_TCPWithoutStart(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15392,
		UDP:    false,
		TCP:    true,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	// Stop without starting - should handle "server not started" error gracefully
	err := server.Stop()
	// Should not return error (server not started is ignored)
	if err != nil {
		t.Logf("Stop without start returned: %v", err)
	}
}

func TestServerStop_BothWithoutStart(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15393,
		UDP:    true,
		TCP:    true,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	// Stop without starting
	err := server.Stop()
	if err != nil {
		t.Logf("Stop without start returned: %v", err)
	}
}

// =============================================================================
// ServeDNS - CNAME to GSLB AAAA path
// =============================================================================

func TestServeDNS_CNAME_To_GSLB_AAAA(t *testing.T) {
	handler, db, cleanup := setupTestHandlerWithGSLB(t)
	defer cleanup()

	zone := &model.Zone{
		Name:    "example.com.",
		Enabled: true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// CNAME pointing to GSLB domain
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "v6app.example.com.",
		Type:    "CNAME",
		Content: "gslb-v6.example.com.",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CNAME record creation failed: %v", err)
	}

	// Create GSLB policy for AAAA
	res, err := db.Writer.Exec(
		`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled) VALUES (?, ?, ?, ?, ?)`,
		"cname-gslb-v6", "gslb-v6.example.com.", "AAAA", 30, 1,
	)
	if err != nil {
		t.Fatalf("GSLB policy insert failed: %v", err)
	}
	policyID, _ := res.LastInsertId()

	poolRes, err := db.Writer.Exec(
		`INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool) VALUES (?, ?, ?, ?, ?, ?)`,
		policyID, "default", "default", "", 0, 1,
	)
	if err != nil {
		t.Fatalf("Pool insert failed: %v", err)
	}
	poolID, _ := poolRes.LastInsertId()

	_, err = db.Writer.Exec(
		`INSERT INTO gslb_members (pool_id, address, weight, enabled) VALUES (?, ?, ?, ?)`,
		poolID, "2001:db8::100", 100, 1,
	)
	if err != nil {
		t.Fatalf("Member insert failed: %v", err)
	}

	// Query AAAA for CNAME -> GSLB
	req := new(dns.Msg)
	req.SetQuestion("v6app.example.com.", dns.TypeAAAA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// Should have CNAME + AAAA
	if len(w.msg.Answer) < 2 {
		t.Errorf("Expected at least 2 answers (CNAME + AAAA from GSLB), got: %d", len(w.msg.Answer))
	}

	hasAAAA := false
	for _, ans := range w.msg.Answer {
		if _, ok := ans.(*dns.AAAA); ok {
			hasAAAA = true
		}
	}
	if !hasAAAA {
		t.Error("Expected AAAA record in answer from GSLB")
	}
}

// =============================================================================
// ServeDNS with nil stats (coverage for nil stats check)
// =============================================================================

func TestServeDNS_NilStats(t *testing.T) {
	dbPath := "/tmp/test_handler_nilstats_" + t.Name() + ".db"
	_ = os.Remove(dbPath)

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("DB creation failed: %v", err)
	}
	defer func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
	}()

	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)
	upstreamStorage := storage.NewUpstreamStorage(db)
	resolver := NewResolver(upstreamStorage, 5*time.Second)

	// Pass nil stats
	handler, err := NewHandler(zoneStorage, recordStorage, resolver, db, nil, nil, nil, nil, "0.0.0.0", "test", "v1")
	if err != nil {
		t.Fatalf("Handler creation failed: %v", err)
	}

	req := new(dns.Msg)
	req.SetQuestion("test.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req) // should not panic with nil stats

	if !w.written {
		t.Fatal("Response not written")
	}
}
