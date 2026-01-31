package storage

import (
	"dns-go/model"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncVersion_IncrementVersion(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

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
	defer db.Close()

	sv := NewSyncVersion(db)

	version, err := sv.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(0), version)
}

func TestSyncVersion_CalculateChecksum(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

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
	defer db.Close()

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
	defer db.Close()

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
	defer db.Close()

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
	defer db.Close()

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
	defer db.Close()

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

func TestSyncVersion_IncrementVersionWithTransaction(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

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
	defer db.Close()

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
	defer db.Close()

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
