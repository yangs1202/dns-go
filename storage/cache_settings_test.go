package storage

import (
	"dns-go/model"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCacheSettings(t *testing.T) {
	db := setupTestDB(t)

	settings, err := db.GetCacheSettings()
	require.NoError(t, err)
	require.NotNil(t, settings)

	// 기본값 확인
	assert.Equal(t, int64(1), settings.ID)
	assert.True(t, settings.Enabled)
	assert.Equal(t, int64(10000), settings.MaxSize)
	assert.Equal(t, int64(300), settings.DefaultTTL)
	assert.Equal(t, int64(60), settings.MinTTL)
	assert.Equal(t, int64(86400), settings.MaxTTL)
	assert.Equal(t, int64(300), settings.NegativeTTL)
	assert.Equal(t, 0.9, settings.PrefetchTrigger)
}

func TestUpdateCacheSettings(t *testing.T) {
	db := setupTestDB(t)

	// 기존 설정 조회
	settings, err := db.GetCacheSettings()
	require.NoError(t, err)

	// 설정 변경
	settings.MaxSize = 20000
	settings.DefaultTTL = 600
	settings.PrefetchTrigger = 0.8

	err = db.UpdateCacheSettings(settings)
	require.NoError(t, err)

	// 변경된 설정 확인
	updated, err := db.GetCacheSettings()
	require.NoError(t, err)

	assert.Equal(t, int64(20000), updated.MaxSize)
	assert.Equal(t, int64(600), updated.DefaultTTL)
	assert.Equal(t, 0.8, updated.PrefetchTrigger)
}

func TestUpdateCacheSettingsValidation(t *testing.T) {
	db := setupTestDB(t)

	tests := []struct {
		name     string
		settings *model.CacheSettings
		wantErr  bool
		errMsg   string
	}{
		{
			name: "max_size 너무 작음",
			settings: &model.CacheSettings{
				MaxSize:         50,
				DefaultTTL:      300,
				MinTTL:          60,
				MaxTTL:          86400,
				PrefetchTrigger: 0.9,
			},
			wantErr: true,
			errMsg:  "max_size는 최소 100 이상이어야 합니다",
		},
		{
			name: "min_ttl 너무 작음",
			settings: &model.CacheSettings{
				MaxSize:         10000,
				DefaultTTL:      300,
				MinTTL:          0,
				MaxTTL:          86400,
				PrefetchTrigger: 0.9,
			},
			wantErr: true,
			errMsg:  "min_ttl은 최소 1초 이상이어야 합니다",
		},
		{
			name: "max_ttl < min_ttl",
			settings: &model.CacheSettings{
				MaxSize:         10000,
				DefaultTTL:      300,
				MinTTL:          1000,
				MaxTTL:          500,
				PrefetchTrigger: 0.9,
			},
			wantErr: true,
			errMsg:  "max_ttl은 min_ttl보다 크거나 같아야 합니다",
		},
		{
			name: "default_ttl < min_ttl",
			settings: &model.CacheSettings{
				MaxSize:         10000,
				DefaultTTL:      50,
				MinTTL:          100,
				MaxTTL:          86400,
				PrefetchTrigger: 0.9,
			},
			wantErr: true,
			errMsg:  "default_ttl은 min_ttl과 max_ttl 사이여야 합니다",
		},
		{
			name: "default_ttl > max_ttl",
			settings: &model.CacheSettings{
				MaxSize:         10000,
				DefaultTTL:      90000,
				MinTTL:          60,
				MaxTTL:          86400,
				PrefetchTrigger: 0.9,
			},
			wantErr: true,
			errMsg:  "default_ttl은 min_ttl과 max_ttl 사이여야 합니다",
		},
		{
			name: "prefetch_trigger < 0",
			settings: &model.CacheSettings{
				MaxSize:         10000,
				DefaultTTL:      300,
				MinTTL:          60,
				MaxTTL:          86400,
				PrefetchTrigger: -0.1,
			},
			wantErr: true,
			errMsg:  "prefetch_trigger는 0과 1 사이여야 합니다",
		},
		{
			name: "prefetch_trigger > 1",
			settings: &model.CacheSettings{
				MaxSize:         10000,
				DefaultTTL:      300,
				MinTTL:          60,
				MaxTTL:          86400,
				PrefetchTrigger: 1.5,
			},
			wantErr: true,
			errMsg:  "prefetch_trigger는 0과 1 사이여야 합니다",
		},
		{
			name: "유효한 설정",
			settings: &model.CacheSettings{
				Enabled:         true,
				MaxSize:         20000,
				DefaultTTL:      600,
				MinTTL:          100,
				MaxTTL:          7200,
				NegativeTTL:     300,
				PrefetchTrigger: 0.8,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.UpdateCacheSettings(tt.settings)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUpdateCacheSettingsInsert(t *testing.T) {
	db := setupTestDB(t)

	// 기존 설정 삭제
	_, err := db.Writer.Exec("DELETE FROM cache_settings WHERE id = 1")
	require.NoError(t, err)

	// 새 설정 삽입 (UPDATE가 실패하면 INSERT)
	settings := &model.CacheSettings{
		Enabled:         true,
		MaxSize:         15000,
		DefaultTTL:      400,
		MinTTL:          50,
		MaxTTL:          7200,
		NegativeTTL:     200,
		PrefetchTrigger: 0.85,
	}

	err = db.UpdateCacheSettings(settings)
	require.NoError(t, err)

	// 삽입된 설정 확인
	inserted, err := db.GetCacheSettings()
	require.NoError(t, err)

	assert.Equal(t, int64(15000), inserted.MaxSize)
	assert.Equal(t, int64(400), inserted.DefaultTTL)
}

func TestCacheSettingsSingleton(t *testing.T) {
	db := setupTestDB(t)

	// 중복 삽입 시도 (id = 1은 singleton이므로 실패해야 함)
	_, err := db.Writer.Exec(`INSERT INTO cache_settings (id, max_size) VALUES (1, 5000)`)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "UNIQUE")
}

func TestGetCacheSettingsNoData(t *testing.T) {
	db := setupTestDB(t)

	// 설정 삭제
	_, err := db.Writer.Exec("DELETE FROM cache_settings WHERE id = 1")
	require.NoError(t, err)

	// 설정 조회 (기본값 반환)
	settings, err := db.GetCacheSettings()
	require.NoError(t, err)
	require.NotNil(t, settings)

	// 기본값 확인
	assert.Equal(t, int64(1), settings.ID)
	assert.True(t, settings.Enabled)
	assert.Equal(t, int64(10000), settings.MaxSize)
}

func TestCacheSettingsDisable(t *testing.T) {
	db := setupTestDB(t)

	settings, err := db.GetCacheSettings()
	require.NoError(t, err)

	// 캐시 비활성화
	settings.Enabled = false
	err = db.UpdateCacheSettings(settings)
	require.NoError(t, err)

	// 확인
	updated, err := db.GetCacheSettings()
	require.NoError(t, err)
	assert.False(t, updated.Enabled)
}

func TestCacheSettingsEdgeCases(t *testing.T) {
	db := setupTestDB(t)

	// min_ttl = max_ttl = default_ttl (경계 케이스)
	settings := &model.CacheSettings{
		Enabled:         true,
		MaxSize:         10000,
		DefaultTTL:      300,
		MinTTL:          300,
		MaxTTL:          300,
		NegativeTTL:     100,
		PrefetchTrigger: 0.9,
	}

	err := db.UpdateCacheSettings(settings)
	require.NoError(t, err)

	updated, err := db.GetCacheSettings()
	require.NoError(t, err)
	assert.Equal(t, int64(300), updated.MinTTL)
	assert.Equal(t, int64(300), updated.MaxTTL)
	assert.Equal(t, int64(300), updated.DefaultTTL)
}

func TestCacheSettingsPrefetchTriggerBoundary(t *testing.T) {
	db := setupTestDB(t)

	tests := []struct {
		name    string
		trigger float64
		wantErr bool
	}{
		{"0.0 (최소)", 0.0, false},
		{"1.0 (최대)", 1.0, false},
		{"0.5 (중간)", 0.5, false},
		{"-0.01 (범위 미만)", -0.01, true},
		{"1.01 (범위 초과)", 1.01, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &model.CacheSettings{
				Enabled:         true,
				MaxSize:         10000,
				DefaultTTL:      300,
				MinTTL:          60,
				MaxTTL:          86400,
				NegativeTTL:     300,
				PrefetchTrigger: tt.trigger,
			}

			err := db.UpdateCacheSettings(settings)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
