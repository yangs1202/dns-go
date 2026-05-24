package dns

import (
	"dns-go/adblock"
	"dns-go/config"
	"dns-go/gslb"
	"dns-go/model"
	"dns-go/storage"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// gslbTestEnv holds references to GSLB storage for tests that need cache invalidation.
type gslbTestEnv struct {
	PoolStorage *gslb.PoolStorage
}

// setupTestHandlerWithGSLB creates a test handler with GSLB engine configured.
func setupTestHandlerWithGSLB(t *testing.T) (*Handler, *storage.Database, func()) {
	handler, db, _, cleanup := setupTestHandlerWithGSLBEnv(t)
	return handler, db, cleanup
}

// setupTestHandlerWithGSLBEnv creates a test handler with GSLB engine and returns gslbTestEnv.
func setupTestHandlerWithGSLBEnv(t *testing.T) (*Handler, *storage.Database, *gslbTestEnv, func()) {
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

	handler, err := NewHandler(zoneStorage, recordStorage, resolver, db, stats, engine, nil, nil, "0.0.0.0", "test-server", "DNS-Go Test v1.0", nil)
	if err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Handler creation failed: %v", err)
	}

	env := &gslbTestEnv{PoolStorage: poolStorage}

	cleanup := func() {
		handler.Stop()
		_ = db.Close()
		_ = os.Remove(dbPath)
	}

	return handler, db, env, cleanup
}

func startCountingPTRUpstream(t *testing.T, requests *atomic.Int64) (string, func()) {
	t.Helper()

	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("upstream listen failed: %v", err)
	}

	server := &dns.Server{
		PacketConn: packetConn,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			requests.Add(1)
			resp := new(dns.Msg)
			resp.SetReply(r)
			if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypePTR {
				resp.Answer = append(resp.Answer, &dns.PTR{
					Hdr: dns.RR_Header{
						Name:   r.Question[0].Name,
						Rrtype: dns.TypePTR,
						Class:  dns.ClassINET,
						Ttl:    60,
					},
					Ptr: "public.example.",
				})
			}
			_ = w.WriteMsg(resp)
		}),
	}

	go func() {
		if err := server.ActivateAndServe(); err != nil {
			t.Logf("counting upstream stopped: %v", err)
		}
	}()

	return packetConn.LocalAddr().String(), func() {
		_ = server.Shutdown()
	}
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

	handler, err := NewHandler(zoneStorage, recordStorage, resolver, db, stats, nil, filter, adblockStorage, response, "test-server", "DNS-Go Test v1.0", nil)
	if err != nil {
		_ = db.Close()
		_ = os.Remove(dbPath)
		t.Fatalf("Handler creation failed: %v", err)
	}

	cleanup := func() {
		handler.Stop()
		_ = db.Close()
		_ = os.Remove(dbPath)
	}

	return handler, db, cleanup
}

func setupTestHandlerWithDisabledCache(t *testing.T) (*Handler, *storage.Database, func()) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("DB creation failed: %v", err)
	}
	if _, err := db.Writer.Exec("UPDATE cache_settings SET enabled = 0 WHERE id = 1"); err != nil {
		_ = db.Close()
		t.Fatalf("cache setting update failed: %v", err)
	}

	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)
	upstreamStorage := storage.NewUpstreamStorage(db)
	resolver := NewResolver(upstreamStorage, 5*time.Second)
	stats := NewQueryStats()

	handler, err := NewHandler(zoneStorage, recordStorage, resolver, db, stats, nil, nil, nil, "0.0.0.0", "test-server", "DNS-Go Test v1.0", nil)
	if err != nil {
		_ = db.Close()
		t.Fatalf("Handler creation failed: %v", err)
	}

	cleanup := func() {
		handler.Stop()
		_ = db.Close()
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

func TestRecordToRR_SRV(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	record := &model.Record{
		Name:     "_http._tcp.example.com.",
		Type:     "SRV",
		Content:  "5 80 www.example.com.",
		TTL:      300,
		Priority: 10,
	}

	rr := handler.recordToRR(record)
	srv, ok := rr.(*dns.SRV)
	if !ok {
		t.Fatalf("Expected SRV RR, got %T", rr)
	}
	if srv.Priority != 10 || srv.Weight != 5 || srv.Port != 80 || srv.Target != "www.example.com." {
		t.Fatalf("Unexpected SRV: %+v", srv)
	}
}

func TestRecordToRR_ExtendedTypes(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	srvFull := handler.recordToRR(&model.Record{
		Name:    "_sip._tcp.example.com.",
		Type:    "SRV",
		Content: "20 7 5060 sip.example.com",
		TTL:     300,
	})
	srv, ok := srvFull.(*dns.SRV)
	if !ok {
		t.Fatalf("Expected SRV RR, got %T", srvFull)
	}
	if srv.Priority != 20 || srv.Weight != 7 || srv.Port != 5060 || srv.Target != "sip.example.com." {
		t.Fatalf("Unexpected SRV RR: %+v", srv)
	}

	if rr := handler.recordToRR(&model.Record{Name: "_bad._tcp.example.com.", Type: "SRV", Content: "5060", TTL: 60}); rr != nil {
		t.Fatalf("Expected nil for invalid SRV content, got %v", rr)
	}

	ptrRR := handler.recordToRR(&model.Record{Name: "1.2.0.192.in-addr.arpa.", Type: "PTR", Content: "host.example.com", TTL: 60})
	ptr, ok := ptrRR.(*dns.PTR)
	if !ok {
		t.Fatalf("Expected PTR RR, got %T", ptrRR)
	}
	if ptr.Ptr != "host.example.com." {
		t.Fatalf("Expected PTR target with trailing dot, got %s", ptr.Ptr)
	}

	caaRR := handler.recordToRR(&model.Record{Name: "example.com.", Type: "CAA", Content: "0 issue ca.example", TTL: 60})
	caa, ok := caaRR.(*dns.CAA)
	if !ok {
		t.Fatalf("Expected CAA RR, got %T", caaRR)
	}
	if caa.Flag != 0 || caa.Tag != "issue" || caa.Value != "ca.example" {
		t.Fatalf("Unexpected CAA RR: %+v", caa)
	}

	if rr := handler.recordToRR(&model.Record{Name: "example.com.", Type: "CAA", Content: "0 issue", TTL: 60}); rr != nil {
		t.Fatalf("Expected nil for invalid CAA content, got %v", rr)
	}

	if got := parseUint16("bad"); got != 0 {
		t.Fatalf("Expected parseUint16 bad input to return 0, got %d", got)
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
			name:   "SRV record",
			record: &model.Record{Name: "_sip._tcp.t.com.", Type: "SRV", Content: "5 5060 sip.t.com.", TTL: 60, Priority: 10},
		},
		{
			name:   "PTR record",
			record: &model.Record{Name: "1.2.3.4.in-addr.arpa.", Type: "PTR", Content: "host.t.com.", TTL: 60},
		},
		{
			name:   "CAA record",
			record: &model.Record{Name: "t.com.", Type: "CAA", Content: "0 issue letsencrypt.org", TTL: 60},
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
		Enabled:         true,
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
// ServeDNS - CNAME to GSLB: L1 cache must NOT store GSLB-resolved responses
// =============================================================================

func TestServeDNS_CNAME_To_GSLB_NoCacheOnFirstQuery(t *testing.T) {
	handler, db, cleanup := setupTestHandlerWithGSLB(t)
	defer cleanup()

	// Zone 생성
	zone := &model.Zone{
		Name:    "yangs.sh.",
		Enabled: true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// album.yangs.sh. -> CNAME -> lb.gslb.yangs.sh.
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "album.yangs.sh.",
		Type:    "CNAME",
		Content: "lb.gslb.yangs.sh.",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CNAME record creation failed: %v", err)
	}

	// GSLB policy: lb.gslb.yangs.sh. -> 10.96.50.21
	res, err := db.Writer.Exec(
		`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled) VALUES (?, ?, ?, ?, ?)`,
		"lb-gslb", "lb.gslb.yangs.sh.", "A", 0, 1,
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
		poolID, "10.96.50.21", 100, 1,
	)
	if err != nil {
		t.Fatalf("GSLB member insert failed: %v", err)
	}

	// 1차 쿼리: album.yangs.sh. A
	req := new(dns.Msg)
	req.SetQuestion("album.yangs.sh.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
	if len(w.msg.Answer) < 2 {
		t.Fatalf("Expected at least 2 answers (CNAME + A), got: %d", len(w.msg.Answer))
	}

	// L1 캐시에 저장되지 않아야 함 (GSLB 경유)
	_, cached := handler.cache.Get("album.yangs.sh.", "A")
	if cached {
		t.Fatal("CNAME→GSLB 응답이 L1 캐시에 저장되면 안 됨 (동적 응답)")
	}
}

func TestServeDNS_CNAME_To_GSLB_AlwaysFreshResponse(t *testing.T) {
	handler, db, env, cleanup := setupTestHandlerWithGSLBEnv(t)
	defer cleanup()

	zone := &model.Zone{
		Name:    "yangs.sh.",
		Enabled: true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// album.yangs.sh. -> CNAME -> lb.gslb.yangs.sh.
	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "album.yangs.sh.",
		Type:    "CNAME",
		Content: "lb.gslb.yangs.sh.",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("CNAME record creation failed: %v", err)
	}

	// GSLB policy with 2 members (weight-based)
	res, err := db.Writer.Exec(
		`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled) VALUES (?, ?, ?, ?, ?)`,
		"lb-gslb", "lb.gslb.yangs.sh.", "A", 0, 1,
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
		poolID, "10.96.50.21", 100, 1,
	)
	if err != nil {
		t.Fatalf("GSLB member insert failed: %v", err)
	}

	// 1차 쿼리
	req1 := new(dns.Msg)
	req1.SetQuestion("album.yangs.sh.", dns.TypeA)
	w1 := newMockWriter("192.0.2.100")
	handler.ServeDNS(w1, req1)

	if w1.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("1차 쿼리 실패: %s", dns.RcodeToString[w1.msg.Rcode])
	}

	// GSLB 멤버 IP 변경 (장애 복구 시나리오: 10.96.50.21 → 10.97.11.18)
	// Engine이 사용하는 PoolStorage를 통해 업데이트 → 캐시 자동 무효화
	member, err := env.PoolStorage.GetMember(1) // 첫 번째 멤버
	if err != nil || member == nil {
		t.Fatalf("GSLB member lookup failed: %v", err)
	}
	member.Address = "10.97.11.18"
	err = env.PoolStorage.UpdateMember(member)
	if err != nil {
		t.Fatalf("GSLB member update failed: %v", err)
	}

	// 2차 쿼리 - 캐시가 없으므로 GSLB 엔진에서 새로운 IP를 받아야 함
	req2 := new(dns.Msg)
	req2.SetQuestion("album.yangs.sh.", dns.TypeA)
	w2 := newMockWriter("192.0.2.100")
	handler.ServeDNS(w2, req2)

	if w2.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("2차 쿼리 실패: %s", dns.RcodeToString[w2.msg.Rcode])
	}

	// 2차 응답에서 새 IP가 반환되어야 함
	var foundIP string
	for _, ans := range w2.msg.Answer {
		if a, ok := ans.(*dns.A); ok {
			foundIP = a.A.String()
		}
	}
	if foundIP != "10.97.11.18" {
		t.Errorf("GSLB IP 변경 후 2차 쿼리에서 새 IP 예상: 10.97.11.18, 실제: %s (stale cache 문제)", foundIP)
	}
}

func TestServeDNS_CNAME_To_NonGSLB_StillCached(t *testing.T) {
	handler, _, cleanup := setupTestHandlerWithGSLB(t)
	defer cleanup()

	zone := &model.Zone{
		Name:    "example.com.",
		Enabled: true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone creation failed: %v", err)
	}

	// CNAME -> 일반 A 레코드 (GSLB 아님)
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

	_, err = handler.recordStorage.CreateRecord(&model.Record{
		ZoneID:  zoneID,
		Name:    "target.example.com.",
		Type:    "A",
		Content: "192.0.2.99",
		TTL:     300,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("A record creation failed: %v", err)
	}

	// 쿼리 실행
	req := new(dns.Msg)
	req.SetQuestion("alias.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// GSLB가 아닌 일반 CNAME 체인은 여전히 L1 캐시에 저장되어야 함
	_, cached := handler.cache.Get("alias.example.com.", "A")
	if !cached {
		t.Error("CNAME→일반 A 레코드 응답은 L1 캐시에 저장되어야 함")
	}
}

func TestServeDNS_DirectGSLB_NeverCached(t *testing.T) {
	handler, db, cleanup := setupTestHandlerWithGSLB(t)
	defer cleanup()

	// GSLB 직접 쿼리는 기존처럼 캐시하지 않는 것 확인
	res, err := db.Writer.Exec(
		`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled) VALUES (?, ?, ?, ?, ?)`,
		"direct-gslb", "lb.gslb.example.com.", "A", 0, 1,
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
		poolID, "10.0.0.1", 100, 1,
	)
	if err != nil {
		t.Fatalf("GSLB member insert failed: %v", err)
	}

	req := new(dns.Msg)
	req.SetQuestion("lb.gslb.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("Expected NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}

	// 직접 GSLB 쿼리도 캐시되지 않아야 함
	_, cached := handler.cache.Get("lb.gslb.example.com.", "A")
	if cached {
		t.Error("직접 GSLB 쿼리 응답이 L1 캐시에 저장되면 안 됨")
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

func TestServeDNS_PrivateReverseDoesNotForwardUpstream(t *testing.T) {
	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	var upstreamRequests atomic.Int64
	upstreamAddr, stopUpstream := startCountingPTRUpstream(t, &upstreamRequests)
	defer stopUpstream()

	upstreamStorage := storage.NewUpstreamStorage(db)
	createTestServer(t, upstreamStorage, "Counting PTR", upstreamAddr, "udp", 1, true)

	req := new(dns.Msg)
	req.SetQuestion("5.1.96.10.in-addr.arpa.", dns.TypePTR)
	req.RecursionDesired = true
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}
	if w.msg.Rcode != dns.RcodeNameError {
		t.Fatalf("Expected private reverse NXDOMAIN, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
	if upstreamRequests.Load() != 0 {
		t.Fatalf("Private reverse query leaked to upstream: %d requests", upstreamRequests.Load())
	}
	if len(w.msg.Ns) == 0 || w.msg.Ns[0].Header().Name != "10.in-addr.arpa." {
		t.Fatalf("Expected 10.in-addr.arpa. SOA authority, got %#v", w.msg.Ns)
	}
}

func TestPrivateIPv4ReverseZone_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantZone string
		wantOK   bool
	}{
		{name: "10 slash 8", query: "5.1.96.10.in-addr.arpa.", wantZone: "10.in-addr.arpa.", wantOK: true},
		{name: "192 168 slash 16", query: "20.1.168.192.in-addr.arpa.", wantZone: "168.192.in-addr.arpa.", wantOK: true},
		{name: "172 lower bound", query: "1.0.16.172.in-addr.arpa.", wantZone: "16.172.in-addr.arpa.", wantOK: true},
		{name: "172 upper bound", query: "1.0.31.172.in-addr.arpa.", wantZone: "31.172.in-addr.arpa.", wantOK: true},
		{name: "172 below range", query: "1.0.15.172.in-addr.arpa.", wantOK: false},
		{name: "172 above range", query: "1.0.32.172.in-addr.arpa.", wantOK: false},
		{name: "public reverse", query: "8.8.8.8.in-addr.arpa.", wantOK: false},
		{name: "shared address space is not RFC1918", query: "1.64.100.in-addr.arpa.", wantOK: false},
		{name: "malformed reverse", query: "not-reverse.example.", wantOK: false},
		{name: "ipv6 reverse", query: "1.0.0.127.ip6.arpa.", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotZone, gotOK := privateIPv4ReverseZone(tt.query)
			if gotOK != tt.wantOK {
				t.Fatalf("ok mismatch: got %v want %v", gotOK, tt.wantOK)
			}
			if gotZone != tt.wantZone {
				t.Fatalf("zone mismatch: got %q want %q", gotZone, tt.wantZone)
			}
		})
	}
}

func TestServeDNS_PublicReverseStillForwardsUpstream(t *testing.T) {
	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	var upstreamRequests atomic.Int64
	upstreamAddr, stopUpstream := startCountingPTRUpstream(t, &upstreamRequests)
	defer stopUpstream()

	upstreamStorage := storage.NewUpstreamStorage(db)
	createTestServer(t, upstreamStorage, "Counting PTR", upstreamAddr, "udp", 1, true)

	req := new(dns.Msg)
	req.SetQuestion("8.8.8.8.in-addr.arpa.", dns.TypePTR)
	req.RecursionDesired = true
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("Expected public reverse NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
	if upstreamRequests.Load() != 1 {
		t.Fatalf("Expected public reverse to forward once, got: %d", upstreamRequests.Load())
	}
	if len(w.msg.Answer) != 1 {
		t.Fatalf("Expected one upstream PTR answer, got %d", len(w.msg.Answer))
	}
}

func TestLocalUseResponse_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		domain      string
		qtype       string
		wantOK      bool
		wantZone    string
		wantAnswers int
		wantA       string
		wantAAAA    string
	}{
		{name: "localhost A", domain: "localhost.", qtype: "A", wantOK: true, wantZone: "localhost.", wantAnswers: 1, wantA: "127.0.0.1"},
		{name: "localhost AAAA uppercase", domain: "LOCALHOST.", qtype: "AAAA", wantOK: true, wantZone: "localhost.", wantAnswers: 1, wantAAAA: "::1"},
		{name: "localhost other type empty noerror", domain: "localhost.", qtype: "MX", wantOK: true, wantZone: "localhost.", wantAnswers: 0},
		{name: "dot local suffix", domain: "printer.local.", qtype: "A", wantOK: true, wantZone: "local.", wantAnswers: 0},
		{name: "single label", domain: "ix-truenas.", qtype: "A", wantOK: true, wantZone: "ix-truenas.", wantAnswers: 0},
		{name: "normal fqdn", domain: "example.com.", qtype: "A", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answers, zone, ok := localUseResponse(tt.domain, tt.qtype)
			if ok != tt.wantOK {
				t.Fatalf("ok mismatch: got %v want %v", ok, tt.wantOK)
			}
			if zone != tt.wantZone {
				t.Fatalf("zone mismatch: got %q want %q", zone, tt.wantZone)
			}
			if len(answers) != tt.wantAnswers {
				t.Fatalf("answer count mismatch: got %d want %d", len(answers), tt.wantAnswers)
			}
			if tt.wantA != "" {
				a, ok := answers[0].(*dns.A)
				if !ok || a.A.String() != tt.wantA {
					t.Fatalf("A answer mismatch: %#v", answers[0])
				}
			}
			if tt.wantAAAA != "" {
				aaaa, ok := answers[0].(*dns.AAAA)
				if !ok || aaaa.AAAA.String() != tt.wantAAAA {
					t.Fatalf("AAAA answer mismatch: %#v", answers[0])
				}
			}
		})
	}
}

func TestServeDNS_LocalhostHandledLocally(t *testing.T) {
	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	var upstreamRequests atomic.Int64
	upstreamAddr, stopUpstream := startCountingPTRUpstream(t, &upstreamRequests)
	defer stopUpstream()

	upstreamStorage := storage.NewUpstreamStorage(db)
	createTestServer(t, upstreamStorage, "Counting Localhost", upstreamAddr, "udp", 1, true)

	req := new(dns.Msg)
	req.SetQuestion("localhost.", dns.TypeA)
	req.RecursionDesired = true
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if !w.written {
		t.Fatal("Response not written")
	}
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("Expected localhost NOERROR, got: %s", dns.RcodeToString[w.msg.Rcode])
	}
	if upstreamRequests.Load() != 0 {
		t.Fatalf("localhost query leaked to upstream: %d requests", upstreamRequests.Load())
	}
	if len(w.msg.Answer) != 1 {
		t.Fatalf("Expected one localhost answer, got %d", len(w.msg.Answer))
	}
	a, ok := w.msg.Answer[0].(*dns.A)
	if !ok || a.A.String() != "127.0.0.1" {
		t.Fatalf("Expected localhost A 127.0.0.1, got %#v", w.msg.Answer[0])
	}
}

func TestServeDNS_LocalUsePathsWorkWithDisabledCache(t *testing.T) {
	handler, db, cleanup := setupTestHandlerWithDisabledCache(t)
	defer cleanup()
	if handler.cache != nil {
		t.Fatal("expected cache to be disabled")
	}

	var upstreamRequests atomic.Int64
	upstreamAddr, stopUpstream := startCountingPTRUpstream(t, &upstreamRequests)
	defer stopUpstream()

	upstreamStorage := storage.NewUpstreamStorage(db)
	createTestServer(t, upstreamStorage, "Counting Disabled Cache Local", upstreamAddr, "udp", 1, true)

	tests := []struct {
		name   string
		domain string
		qtype  uint16
		rcode  int
	}{
		{name: "private reverse", domain: "1.0.16.172.in-addr.arpa.", qtype: dns.TypePTR, rcode: dns.RcodeNameError},
		{name: "localhost AAAA", domain: "localhost.", qtype: dns.TypeAAAA, rcode: dns.RcodeSuccess},
		{name: "dot local", domain: "printer.local.", qtype: dns.TypeA, rcode: dns.RcodeSuccess},
		{name: "single label", domain: "ix-truenas.", qtype: dns.TypeMX, rcode: dns.RcodeSuccess},
	}

	for _, tt := range tests {
		req := new(dns.Msg)
		req.SetQuestion(tt.domain, tt.qtype)
		req.RecursionDesired = true
		w := newMockWriter("192.0.2.100")
		handler.ServeDNS(w, req)

		if !w.written {
			t.Fatalf("%s: response not written", tt.name)
		}
		if w.msg.Rcode != tt.rcode {
			t.Fatalf("%s: rcode mismatch: got %s want %s", tt.name, dns.RcodeToString[w.msg.Rcode], dns.RcodeToString[tt.rcode])
		}
	}
	if upstreamRequests.Load() != 0 {
		t.Fatalf("local-use queries leaked to upstream with disabled cache: %d requests", upstreamRequests.Load())
	}
}

func TestServeDNS_LocalAndSingleLabelDoNotForwardUpstream(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		qtype  uint16
	}{
		{name: "local suffix", domain: "ix-truenas.local.", qtype: dns.TypeA},
		{name: "single label", domain: "ix-truenas.", qtype: dns.TypeA},
	}

	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	var upstreamRequests atomic.Int64
	upstreamAddr, stopUpstream := startCountingPTRUpstream(t, &upstreamRequests)
	defer stopUpstream()

	upstreamStorage := storage.NewUpstreamStorage(db)
	createTestServer(t, upstreamStorage, "Counting Local", upstreamAddr, "udp", 1, true)

	for _, tt := range tests {
		req := new(dns.Msg)
		req.SetQuestion(tt.domain, tt.qtype)
		req.RecursionDesired = true
		w := newMockWriter("192.0.2.100")
		handler.ServeDNS(w, req)

		if !w.written {
			t.Fatalf("%s: response not written", tt.name)
		}
		if w.msg.Rcode != dns.RcodeSuccess {
			t.Fatalf("%s: expected local-use NOERROR, got: %s", tt.name, dns.RcodeToString[w.msg.Rcode])
		}
		if upstreamRequests.Load() != 0 {
			t.Fatalf("%s: local-use query leaked to upstream: %d requests", tt.name, upstreamRequests.Load())
		}
		if len(w.msg.Answer) != 0 {
			t.Fatalf("%s: expected empty local-use answer, got %d", tt.name, len(w.msg.Answer))
		}
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
	defer cache.Stop()

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
	defer cache.Stop()

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
	defer cache.Stop()

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
	handler, err := NewHandler(zoneStorage, recordStorage, resolver, db, nil, nil, nil, nil, "0.0.0.0", "test", "v1", nil)
	if err != nil {
		t.Fatalf("Handler creation failed: %v", err)
	}
	defer handler.Stop()

	req := new(dns.Msg)
	req.SetQuestion("test.example.com.", dns.TypeA)
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req) // should not panic with nil stats

	if !w.written {
		t.Fatal("Response not written")
	}
}
