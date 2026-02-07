package storage

import (
	"dns-go/model"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncVersion_IncrementVersion(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// 초기 버전 확인
	version, err := sv.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(0), version)

	// 버전 증가
	err = sv.IncrementVersion(nil)
	require.NoError(t, err)

	// 버전 확인
	version, err = sv.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(1), version)

	// 여러 번 증가
	for i := 0; i < 5; i++ {
		err = sv.IncrementVersion(nil)
		require.NoError(t, err)
	}

	version, err = sv.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(6), version)
}

func TestSyncVersion_GetVersion(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	version, err := sv.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(0), version)
}

func TestSyncVersion_CalculateChecksum(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// 빈 데이터 체크섬
	checksum1, err := sv.CalculateChecksum()
	require.NoError(t, err)
	assert.NotEmpty(t, checksum1)

	// Zone 추가
	zoneStorage := NewZoneStorage(db)
	zone := &model.Zone{
		Name:       "example.com.",
		SOAMname:   "ns1.example.com.",
		SOARname:   "admin.example.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}
	zoneID, err := zoneStorage.CreateZone(zone)
	require.NoError(t, err)

	// 체크섬 변경 확인
	checksum2, err := sv.CalculateChecksum()
	require.NoError(t, err)
	assert.NotEmpty(t, checksum2)
	assert.NotEqual(t, checksum1, checksum2, "Zone 추가 후 체크섬이 변경되어야 함")

	// Record 추가
	recordStorage := NewRecordStorage(db)
	record := &model.Record{
		ZoneID:   zoneID,
		Name:     "www.example.com.",
		Type:     "A",
		Content:  "10.0.0.100",
		TTL:      300,
		Priority: 0,
		Enabled:  true,
	}
	_, err = recordStorage.CreateRecord(record)
	require.NoError(t, err)

	// 체크섬 재변경 확인
	checksum3, err := sv.CalculateChecksum()
	require.NoError(t, err)
	assert.NotEmpty(t, checksum3)
	assert.NotEqual(t, checksum2, checksum3, "Record 추가 후 체크섬이 변경되어야 함")
}

func TestSyncVersion_UpdateChecksum(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// 초기 체크섬 없음
	checksum, err := sv.GetChecksum()
	require.NoError(t, err)
	assert.Empty(t, checksum)

	// 체크섬 업데이트
	err = sv.UpdateChecksum()
	require.NoError(t, err)

	// 체크섬 확인
	checksum, err = sv.GetChecksum()
	require.NoError(t, err)
	assert.NotEmpty(t, checksum)
}

func TestSyncVersion_GetSyncState(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// 초기 상태
	state, err := sv.GetSyncState()
	require.NoError(t, err)
	assert.Equal(t, int64(0), state["version"])
	assert.Empty(t, state["checksum"])

	// 버전 증가
	err = sv.IncrementVersion(nil)
	require.NoError(t, err)

	// 체크섬 업데이트
	err = sv.UpdateChecksum()
	require.NoError(t, err)

	// 상태 확인
	state, err = sv.GetSyncState()
	require.NoError(t, err)
	assert.Equal(t, int64(1), state["version"])
	assert.NotEmpty(t, state["checksum"])
}

func TestSyncVersion_GetAllZones(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)
	zoneStorage := NewZoneStorage(db)

	// 빈 상태
	zones, err := sv.GetAllZones()
	require.NoError(t, err)
	assert.Empty(t, zones)

	// Zone 생성
	zone := &model.Zone{
		Name:       "example.com.",
		SOAMname:   "ns1.example.com.",
		SOARname:   "admin.example.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}
	_, err = zoneStorage.CreateZone(zone)
	require.NoError(t, err)

	// Zone 조회
	zones, err = sv.GetAllZones()
	require.NoError(t, err)
	assert.Len(t, zones, 1)
	assert.Equal(t, "example.com.", zones[0]["name"])
}

func TestSyncVersion_GetAllRecords(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)
	zoneStorage := NewZoneStorage(db)
	recordStorage := NewRecordStorage(db)

	// Zone 생성
	zone := &model.Zone{
		Name:       "example.com.",
		SOAMname:   "ns1.example.com.",
		SOARname:   "admin.example.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}
	zoneID, err := zoneStorage.CreateZone(zone)
	require.NoError(t, err)

	// 빈 상태
	records, err := sv.GetAllRecords()
	require.NoError(t, err)
	assert.Empty(t, records)

	// Record 생성
	record := &model.Record{
		ZoneID:   zoneID,
		Name:     "www.example.com.",
		Type:     "A",
		Content:  "10.0.0.100",
		TTL:      300,
		Priority: 0,
		Enabled:  true,
	}
	_, err = recordStorage.CreateRecord(record)
	require.NoError(t, err)

	// Record 조회
	records, err = sv.GetAllRecords()
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Equal(t, "www.example.com.", records[0]["name"])
	assert.Equal(t, "A", records[0]["type"])
}

func TestSyncVersion_GetAllUpstreams(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)
	upstreamStorage := NewUpstreamStorage(db)

	// 빈 상태
	upstreams, err := sv.GetAllUpstreams()
	require.NoError(t, err)
	assert.Empty(t, upstreams)

	// Upstream 생성
	upstream := &model.UpstreamServer{
		Name:     "Google DNS",
		Address:  "8.8.8.8:53",
		Protocol: "udp",
		Priority: 10,
		Enabled:  true,
	}
	_, err = upstreamStorage.CreateUpstreamServer(upstream)
	require.NoError(t, err)

	// Upstream 조회
	upstreams, err = sv.GetAllUpstreams()
	require.NoError(t, err)
	assert.Len(t, upstreams, 1)
	assert.Equal(t, "Google DNS", upstreams[0]["name"])
}

func TestSyncVersion_GetAllGSLBPolicies_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	policies, err := sv.GetAllGSLBPolicies()
	require.NoError(t, err)
	assert.Empty(t, policies)
}

func TestSyncVersion_GetAllGSLBPolicies_WithData(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// Insert GSLB policy
	_, err := db.Writer.Exec(`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled)
		VALUES (?, ?, ?, ?, ?)`, "policy1", "app.example.com.", "A", 60, 1)
	require.NoError(t, err)

	_, err = db.Writer.Exec(`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled)
		VALUES (?, ?, ?, ?, ?)`, "policy2", "api.example.com.", "AAAA", 120, 0)
	require.NoError(t, err)

	policies, err := sv.GetAllGSLBPolicies()
	require.NoError(t, err)
	require.Len(t, policies, 2)

	assert.Equal(t, "policy1", policies[0]["name"])
	assert.Equal(t, "app.example.com.", policies[0]["domain"])
	assert.Equal(t, "A", policies[0]["record_type"])
	assert.Equal(t, 60, policies[0]["ttl"])
	assert.Equal(t, 1, policies[0]["enabled"])

	assert.Equal(t, "policy2", policies[1]["name"])
	assert.Equal(t, "api.example.com.", policies[1]["domain"])
}

func TestSyncVersion_GetAllGSLBPools_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	pools, err := sv.GetAllGSLBPools()
	require.NoError(t, err)
	assert.Empty(t, pools)
}

func TestSyncVersion_GetAllGSLBPools_WithData(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// Insert policy first (foreign key)
	result, err := db.Writer.Exec(`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled)
		VALUES (?, ?, ?, ?, ?)`, "policy1", "app.example.com.", "A", 60, 1)
	require.NoError(t, err)
	policyID, err := result.LastInsertId()
	require.NoError(t, err)

	// Insert pools
	_, err = db.Writer.Exec(`INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool)
		VALUES (?, ?, ?, ?, ?, ?)`, policyID, "pool-us", "geo", "US", 10, 0)
	require.NoError(t, err)

	_, err = db.Writer.Exec(`INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool)
		VALUES (?, ?, ?, ?, ?, ?)`, policyID, "pool-eu", "geo", "EU", 20, 1)
	require.NoError(t, err)

	pools, err := sv.GetAllGSLBPools()
	require.NoError(t, err)
	require.Len(t, pools, 2)

	assert.Equal(t, "pool-us", pools[0]["name"])
	assert.Equal(t, "geo", pools[0]["match_type"])
	assert.Equal(t, "US", pools[0]["match_value"])
	assert.Equal(t, 10, pools[0]["priority"])
	assert.Equal(t, 0, pools[0]["fallback_pool"])

	assert.Equal(t, "pool-eu", pools[1]["name"])
	assert.Equal(t, 1, pools[1]["fallback_pool"])
}

func TestSyncVersion_GetAllGSLBMembers_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	members, err := sv.GetAllGSLBMembers()
	require.NoError(t, err)
	assert.Empty(t, members)
}

func TestSyncVersion_GetAllGSLBMembers_WithData(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// Insert policy and pool first (foreign keys)
	policyResult, err := db.Writer.Exec(`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled)
		VALUES (?, ?, ?, ?, ?)`, "policy1", "app.example.com.", "A", 60, 1)
	require.NoError(t, err)
	policyID, err := policyResult.LastInsertId()
	require.NoError(t, err)

	poolResult, err := db.Writer.Exec(`INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool)
		VALUES (?, ?, ?, ?, ?, ?)`, policyID, "pool-us", "geo", "US", 10, 0)
	require.NoError(t, err)
	poolID, err := poolResult.LastInsertId()
	require.NoError(t, err)

	// Insert members
	_, err = db.Writer.Exec(`INSERT INTO gslb_members (pool_id, address, weight, enabled)
		VALUES (?, ?, ?, ?)`, poolID, "10.0.1.1", 100, 1)
	require.NoError(t, err)

	_, err = db.Writer.Exec(`INSERT INTO gslb_members (pool_id, address, weight, enabled)
		VALUES (?, ?, ?, ?)`, poolID, "10.0.1.2", 50, 0)
	require.NoError(t, err)

	members, err := sv.GetAllGSLBMembers()
	require.NoError(t, err)
	require.Len(t, members, 2)

	assert.Equal(t, "10.0.1.1", members[0]["address"])
	assert.Equal(t, 100, members[0]["weight"])
	assert.Equal(t, 1, members[0]["enabled"])

	assert.Equal(t, "10.0.1.2", members[1]["address"])
	assert.Equal(t, 50, members[1]["weight"])
	assert.Equal(t, 0, members[1]["enabled"])
}

func TestSyncVersion_GetAllHealthChecks_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	checks, err := sv.GetAllHealthChecks()
	require.NoError(t, err)
	assert.Empty(t, checks)
}

func TestSyncVersion_GetAllHealthChecks_WithData(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// Insert policy (foreign key)
	policyResult, err := db.Writer.Exec(`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled)
		VALUES (?, ?, ?, ?, ?)`, "policy1", "app.example.com.", "A", 60, 1)
	require.NoError(t, err)
	policyID, err := policyResult.LastInsertId()
	require.NoError(t, err)

	// Insert health checks
	_, err = db.Writer.Exec(`INSERT INTO health_checks (policy_id, check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, policyID, "http", "http://10.0.1.1/health", 30, 5, 3, 2, 1)
	require.NoError(t, err)

	_, err = db.Writer.Exec(`INSERT INTO health_checks (policy_id, check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, policyID, "tcp", "10.0.1.2:80", 60, 10, 2, 3, 0)
	require.NoError(t, err)

	checks, err := sv.GetAllHealthChecks()
	require.NoError(t, err)
	require.Len(t, checks, 2)

	assert.Equal(t, "http", checks[0]["check_type"])
	assert.Equal(t, "http://10.0.1.1/health", checks[0]["target"])
	assert.Equal(t, 30, checks[0]["interval_sec"])
	assert.Equal(t, 5, checks[0]["timeout_sec"])
	assert.Equal(t, 3, checks[0]["healthy_threshold"])
	assert.Equal(t, 2, checks[0]["unhealthy_threshold"])
	assert.Equal(t, 1, checks[0]["enabled"])

	assert.Equal(t, "tcp", checks[1]["check_type"])
	assert.Equal(t, "10.0.1.2:80", checks[1]["target"])
	assert.Equal(t, 0, checks[1]["enabled"])
}

func TestSyncVersion_CalculateChecksum_WithGSLBData(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// Calculate checksum with empty GSLB data
	checksum1, err := sv.CalculateChecksum()
	require.NoError(t, err)
	assert.NotEmpty(t, checksum1)

	// Add GSLB policy
	_, err = db.Writer.Exec(`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled)
		VALUES (?, ?, ?, ?, ?)`, "policy1", "app.example.com.", "A", 60, 1)
	require.NoError(t, err)

	// Checksum should change after adding GSLB data
	checksum2, err := sv.CalculateChecksum()
	require.NoError(t, err)
	assert.NotEmpty(t, checksum2)
	assert.NotEqual(t, checksum1, checksum2, "Checksum should change after adding GSLB policy")
}

// TestSyncVersion_CalculateChecksum_FullGSLBData tests checksum with all GSLB tables populated
func TestSyncVersion_CalculateChecksum_FullGSLBData(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)
	zoneStorage := NewZoneStorage(db)
	recordStorage := NewRecordStorage(db)
	upstreamStorage := NewUpstreamStorage(db)

	// Add Zone
	zone := &model.Zone{
		Name: "app.example.com.", SOAMname: "ns1.app.example.com.", SOARname: "admin.app.example.com.",
		SOASerial: 1, SOARefresh: 3600, SOARetry: 900, SOAExpire: 86400, SOAMinimum: 300, Enabled: true,
	}
	zoneID, err := zoneStorage.CreateZone(zone)
	require.NoError(t, err)

	// Add Record
	record := &model.Record{
		ZoneID: zoneID, Name: "www.app.example.com.", Type: "A", Content: "10.0.0.1",
		TTL: 300, Priority: 0, Enabled: true,
	}
	_, err = recordStorage.CreateRecord(record)
	require.NoError(t, err)

	// Add Upstream
	upstream := &model.UpstreamServer{Name: "DNS1", Address: "8.8.8.8:53", Protocol: "udp", Priority: 10, Enabled: true}
	_, err = upstreamStorage.CreateUpstreamServer(upstream)
	require.NoError(t, err)

	// Add GSLB policy
	policyResult, err := db.Writer.Exec(`INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled)
		VALUES (?, ?, ?, ?, ?)`, "policy1", "app.example.com.", "A", 60, 1)
	require.NoError(t, err)
	policyID, err := policyResult.LastInsertId()
	require.NoError(t, err)

	// Add GSLB pool
	poolResult, err := db.Writer.Exec(`INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool)
		VALUES (?, ?, ?, ?, ?, ?)`, policyID, "pool-us", "geo", "US", 10, 0)
	require.NoError(t, err)
	poolID, err := poolResult.LastInsertId()
	require.NoError(t, err)

	// Add GSLB member
	_, err = db.Writer.Exec(`INSERT INTO gslb_members (pool_id, address, weight, enabled)
		VALUES (?, ?, ?, ?)`, poolID, "10.0.1.1", 100, 1)
	require.NoError(t, err)

	// Add health check
	_, err = db.Writer.Exec(`INSERT INTO health_checks (policy_id, check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, policyID, "http", "http://10.0.1.1/health", 30, 5, 3, 2, 1)
	require.NoError(t, err)

	// Calculate checksum with all data populated
	checksum, err := sv.CalculateChecksum()
	require.NoError(t, err)
	assert.NotEmpty(t, checksum)
	assert.Len(t, checksum, 64) // SHA256 hex string is 64 chars

	// Same data should produce same checksum
	checksum2, err := sv.CalculateChecksum()
	require.NoError(t, err)
	assert.Equal(t, checksum, checksum2)

	// UpdateChecksum should work with full data
	err = sv.UpdateChecksum()
	require.NoError(t, err)

	stored, err := sv.GetChecksum()
	require.NoError(t, err)
	assert.Equal(t, checksum, stored)
}

// === Error path tests - targeted CalculateChecksum sub-errors ===

func TestSyncVersion_CalculateChecksum_GSLBPoliciesError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)

	// Drop gslb_policies table to cause error at that specific point
	_, err := db.Writer.Exec("DROP TABLE health_checks")
	require.NoError(t, err)
	_, err = db.Writer.Exec("DROP TABLE gslb_members")
	require.NoError(t, err)
	_, err = db.Writer.Exec("DROP TABLE gslb_pools")
	require.NoError(t, err)
	_, err = db.Writer.Exec("DROP TABLE gslb_policies")
	require.NoError(t, err)

	_, err = sv.CalculateChecksum()
	assert.Error(t, err)
}

func TestSyncVersion_CalculateChecksum_GSLBPoolsError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)

	// Drop gslb_pools to trigger error after policies succeed
	_, err := db.Writer.Exec("DROP TABLE health_checks")
	require.NoError(t, err)
	_, err = db.Writer.Exec("DROP TABLE gslb_members")
	require.NoError(t, err)
	_, err = db.Writer.Exec("DROP TABLE gslb_pools")
	require.NoError(t, err)

	_, err = sv.CalculateChecksum()
	assert.Error(t, err)
}

func TestSyncVersion_CalculateChecksum_GSLBMembersError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)

	// Drop gslb_members to trigger error after pools succeed
	_, err := db.Writer.Exec("DROP TABLE health_checks")
	require.NoError(t, err)
	_, err = db.Writer.Exec("DROP TABLE gslb_members")
	require.NoError(t, err)

	_, err = sv.CalculateChecksum()
	assert.Error(t, err)
}

func TestSyncVersion_CalculateChecksum_HealthChecksError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)

	// Drop health_checks to trigger error after members succeed
	_, err := db.Writer.Exec("DROP TABLE health_checks")
	require.NoError(t, err)

	_, err = sv.CalculateChecksum()
	assert.Error(t, err)
}

// === Error path tests (using closed DB) ===

func TestSyncVersion_GetVersion_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetVersion()
	assert.Error(t, err)
}

func TestSyncVersion_GetChecksum_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetChecksum()
	assert.Error(t, err)
}

func TestSyncVersion_GetSyncState_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetSyncState()
	assert.Error(t, err)
}

func TestSyncVersion_IncrementVersion_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Writer.Close()

	err := sv.IncrementVersion(nil)
	assert.Error(t, err)
}

func TestSyncVersion_CalculateChecksum_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.CalculateChecksum()
	assert.Error(t, err)
}

func TestSyncVersion_UpdateChecksum_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	err := sv.UpdateChecksum()
	assert.Error(t, err)
}

func TestSyncVersion_GetAllZones_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetAllZones()
	assert.Error(t, err)
}

func TestSyncVersion_GetAllRecords_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetAllRecords()
	assert.Error(t, err)
}

func TestSyncVersion_GetAllUpstreams_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetAllUpstreams()
	assert.Error(t, err)
}

func TestSyncVersion_GetAllGSLBPolicies_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetAllGSLBPolicies()
	assert.Error(t, err)
}

func TestSyncVersion_GetAllGSLBPools_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetAllGSLBPools()
	assert.Error(t, err)
}

func TestSyncVersion_GetAllGSLBMembers_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetAllGSLBMembers()
	assert.Error(t, err)
}

func TestSyncVersion_GetAllHealthChecks_DBError(t *testing.T) {
	db := setupTestDB(t)
	sv := NewSyncVersion(db)
	_ = db.Reader.Close()

	_, err := sv.GetAllHealthChecks()
	assert.Error(t, err)
}

func TestSyncVersion_IncrementVersionWithTransaction(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// 트랜잭션 내에서 버전 증가
	tx, err := db.Writer.Begin()
	require.NoError(t, err)

	err = sv.IncrementVersion(tx)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// 버전 확인
	version, err := sv.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(1), version)
}

func TestSyncVersion_IncrementVersionRollback(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)

	// 트랜잭션 롤백
	tx, err := db.Writer.Begin()
	require.NoError(t, err)

	err = sv.IncrementVersion(tx)
	require.NoError(t, err)

	err = tx.Rollback()
	require.NoError(t, err)

	// 버전 확인 (변경 없어야 함)
	version, err := sv.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(0), version)
}

func TestSyncVersion_ChecksumConsistency(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	sv := NewSyncVersion(db)
	zoneStorage := NewZoneStorage(db)

	// Zone 추가
	zone := &model.Zone{
		Name:       "example.com.",
		SOAMname:   "ns1.example.com.",
		SOARname:   "admin.example.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}
	_, err := zoneStorage.CreateZone(zone)
	require.NoError(t, err)

	// 체크섬 계산 (2번)
	checksum1, err := sv.CalculateChecksum()
	require.NoError(t, err)

	checksum2, err := sv.CalculateChecksum()
	require.NoError(t, err)

	// 동일한 데이터는 동일한 체크섬
	assert.Equal(t, checksum1, checksum2, "동일한 데이터는 동일한 체크섬을 가져야 함")
}
