package gslb

import (
	"database/sql"
	"dns-go/model"
	"dns-go/storage"
	"fmt"
)

type HealthCheckStorage struct {
	db *storage.Database
}

func NewHealthCheckStorage(db *storage.Database) *HealthCheckStorage {
	return &HealthCheckStorage{db: db}
}

func (s *HealthCheckStorage) GetHealthCheck(id int64) (*model.HealthCheck, error) {
	query := `SELECT id, policy_id, check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold, enabled
		FROM health_checks WHERE id = ?`
	var hc model.HealthCheck
	err := s.db.Reader.QueryRow(query, id).Scan(
		&hc.ID,
		&hc.PolicyID,
		&hc.CheckType,
		&hc.Target,
		&hc.IntervalSec,
		&hc.TimeoutSec,
		&hc.HealthyThreshold,
		&hc.UnhealthyThreshold,
		&hc.Enabled,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("헬스체크 조회 실패: %w", err)
	}
	return &hc, nil
}

func (s *HealthCheckStorage) GetHealthCheckByPolicy(policyID int64) (*model.HealthCheck, error) {
	query := `SELECT id, policy_id, check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold, enabled
		FROM health_checks WHERE policy_id = ?`
	var hc model.HealthCheck
	err := s.db.Reader.QueryRow(query, policyID).Scan(
		&hc.ID,
		&hc.PolicyID,
		&hc.CheckType,
		&hc.Target,
		&hc.IntervalSec,
		&hc.TimeoutSec,
		&hc.HealthyThreshold,
		&hc.UnhealthyThreshold,
		&hc.Enabled,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("헬스체크 조회 실패: %w", err)
	}
	return &hc, nil
}

func (s *HealthCheckStorage) ListHealthChecks() ([]*model.HealthCheck, error) {
	query := `SELECT id, policy_id, check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold, enabled
		FROM health_checks ORDER BY id`
	rows, err := s.db.Reader.Query(query)
	if err != nil {
		return nil, fmt.Errorf("헬스체크 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var checks []*model.HealthCheck
	for rows.Next() {
		var hc model.HealthCheck
		if err := rows.Scan(
			&hc.ID,
			&hc.PolicyID,
			&hc.CheckType,
			&hc.Target,
			&hc.IntervalSec,
			&hc.TimeoutSec,
			&hc.HealthyThreshold,
			&hc.UnhealthyThreshold,
			&hc.Enabled,
		); err != nil {
			return nil, fmt.Errorf("헬스체크 스캔 실패: %w", err)
		}
		checks = append(checks, &hc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("헬스체크 행 반복 실패: %w", err)
	}
	return checks, nil
}

func (s *HealthCheckStorage) CreateHealthCheck(check *model.HealthCheck) (int64, error) {
	applyDefaults(check)
	query := `INSERT INTO health_checks (policy_id, check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := s.db.Writer.Exec(query,
		check.PolicyID,
		check.CheckType,
		check.Target,
		check.IntervalSec,
		check.TimeoutSec,
		check.HealthyThreshold,
		check.UnhealthyThreshold,
		check.Enabled,
	)
	if err != nil {
		return 0, fmt.Errorf("헬스체크 생성 실패: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("헬스체크 ID 확인 실패: %w", err)
	}
	return id, nil
}

func (s *HealthCheckStorage) UpdateHealthCheck(check *model.HealthCheck) error {
	applyDefaults(check)
	query := `UPDATE health_checks SET check_type = ?, target = ?, interval_sec = ?, timeout_sec = ?, healthy_threshold = ?, unhealthy_threshold = ?, enabled = ?
		WHERE id = ?`
	result, err := s.db.Writer.Exec(query,
		check.CheckType,
		check.Target,
		check.IntervalSec,
		check.TimeoutSec,
		check.HealthyThreshold,
		check.UnhealthyThreshold,
		check.Enabled,
		check.ID,
	)
	if err != nil {
		return fmt.Errorf("헬스체크 업데이트 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("헬스체크를 찾을 수 없습니다")
	}
	return nil
}

func (s *HealthCheckStorage) DeleteHealthCheck(id int64) error {
	result, err := s.db.Writer.Exec("DELETE FROM health_checks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("헬스체크 삭제 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("헬스체크를 찾을 수 없습니다")
	}
	return nil
}

func applyDefaults(check *model.HealthCheck) {
	if check.CheckType == "" {
		check.CheckType = "tcp"
	}
	if check.IntervalSec == 0 {
		check.IntervalSec = 10
	}
	if check.TimeoutSec == 0 {
		check.TimeoutSec = 5
	}
	if check.HealthyThreshold == 0 {
		check.HealthyThreshold = 3
	}
	if check.UnhealthyThreshold == 0 {
		check.UnhealthyThreshold = 2
	}
}
