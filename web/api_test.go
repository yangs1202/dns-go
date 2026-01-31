package web

import (
	"testing"

	"dns-go/storage"

	"github.com/stretchr/testify/assert"
)

func TestNewAPI(t *testing.T) {
	db := storage.SetupTestDB(t)
	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)

	api := NewAPI(
		zoneStorage,
		recordStorage,
		nil, // upstreamStorage
		db,
		nil, // dnsHandler
		nil, // queryStats
		nil, // policyStorage
		nil, // poolStorage
		nil, // adblockStorage
		nil, // adblockSyncer
		nil, // adblockFilter
		nil, // healthCheckStorage
		nil, // healthStatus
		nil, // healthWorker
	)

	assert.NotNil(t, api)
	assert.Equal(t, zoneStorage, api.zoneStorage)
	assert.Equal(t, recordStorage, api.recordStorage)
	assert.Equal(t, db, api.db)
	assert.False(t, api.readOnly) // Default should be false
}

func TestSetReadOnly(t *testing.T) {
	api, _ := setupTestAPI(t)

	// Initially should be false
	assert.False(t, api.readOnly)

	// Set to true
	api.SetReadOnly(true)
	assert.True(t, api.readOnly)

	// Set back to false
	api.SetReadOnly(false)
	assert.False(t, api.readOnly)
}

func TestIsReadOnly(t *testing.T) {
	api, _ := setupTestAPI(t)

	// Initially should be false
	assert.False(t, api.IsReadOnly())

	// Set to true and check
	api.SetReadOnly(true)
	assert.True(t, api.IsReadOnly())

	// Set to false and check
	api.SetReadOnly(false)
	assert.False(t, api.IsReadOnly())
}

func TestReadOnlyMode_Integration(t *testing.T) {
	api, _ := setupTestAPI(t)

	tests := []struct {
		name       string
		readOnly   bool
		wantResult bool
	}{
		{
			name:       "Read-write mode",
			readOnly:   false,
			wantResult: false,
		},
		{
			name:       "Read-only mode",
			readOnly:   true,
			wantResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api.SetReadOnly(tt.readOnly)
			assert.Equal(t, tt.wantResult, api.IsReadOnly())
		})
	}
}
