package storage

import (
	"database/sql"
	"dns-go/model"
	"fmt"
	"time"
)

// GetCacheSettings는 캐시 설정을 조회합니다 (Singleton)
func (db *Database) GetCacheSettings() (*model.CacheSettings, error) {
	query := `SELECT id, enabled, max_size, default_ttl, min_ttl, max_ttl, negative_ttl, prefetch_trigger, updated_at
	          FROM cache_settings WHERE id = 1`

	var settings model.CacheSettings
	err := db.Reader.QueryRow(query).Scan(
		&settings.ID,
		&settings.Enabled,
		&settings.MaxSize,
		&settings.DefaultTTL,
		&settings.MinTTL,
		&settings.MaxTTL,
		&settings.NegativeTTL,
		&settings.PrefetchTrigger,
		&settings.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		// 기본 설정 반환
		return &model.CacheSettings{
			ID:              1,
			Enabled:         true,
			MaxSize:         10000,
			DefaultTTL:      300,
			MinTTL:          60,
			MaxTTL:          86400,
			NegativeTTL:     300,
			PrefetchTrigger: 0.9,
			UpdatedAt:       time.Now(),
		}, nil
	}

	if err != nil {
		return nil, fmt.Errorf("캐시 설정 조회 실패: %w", err)
	}

	return &settings, nil
}

// UpdateCacheSettings는 캐시 설정을 업데이트합니다
func (db *Database) UpdateCacheSettings(settings *model.CacheSettings) error {
	// 유효성 검증
	if settings.MaxSize < 100 {
		return fmt.Errorf("max_size는 최소 100 이상이어야 합니다")
	}
	if settings.MinTTL < 1 {
		return fmt.Errorf("min_ttl은 최소 1초 이상이어야 합니다")
	}
	if settings.MaxTTL < settings.MinTTL {
		return fmt.Errorf("max_ttl은 min_ttl보다 크거나 같아야 합니다")
	}
	if settings.DefaultTTL < settings.MinTTL || settings.DefaultTTL > settings.MaxTTL {
		return fmt.Errorf("default_ttl은 min_ttl과 max_ttl 사이여야 합니다")
	}
	if settings.PrefetchTrigger < 0 || settings.PrefetchTrigger > 1 {
		return fmt.Errorf("prefetch_trigger는 0과 1 사이여야 합니다")
	}

	query := `UPDATE cache_settings
	          SET enabled = ?, max_size = ?, default_ttl = ?, min_ttl = ?, max_ttl = ?,
	              negative_ttl = ?, prefetch_trigger = ?, updated_at = CURRENT_TIMESTAMP
	          WHERE id = 1`

	result, err := db.Writer.Exec(query,
		settings.Enabled,
		settings.MaxSize,
		settings.DefaultTTL,
		settings.MinTTL,
		settings.MaxTTL,
		settings.NegativeTTL,
		settings.PrefetchTrigger,
	)

	if err != nil {
		return fmt.Errorf("캐시 설정 업데이트 실패: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}

	if rows == 0 {
		// 설정이 없으면 삽입
		insertQuery := `INSERT INTO cache_settings (id, enabled, max_size, default_ttl, min_ttl, max_ttl, negative_ttl, prefetch_trigger)
		                VALUES (1, ?, ?, ?, ?, ?, ?, ?)`

		_, err = db.Writer.Exec(insertQuery,
			settings.Enabled,
			settings.MaxSize,
			settings.DefaultTTL,
			settings.MinTTL,
			settings.MaxTTL,
			settings.NegativeTTL,
			settings.PrefetchTrigger,
		)

		if err != nil {
			return fmt.Errorf("캐시 설정 삽입 실패: %w", err)
		}
	}

	return nil
}
