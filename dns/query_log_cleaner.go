package dns

import (
	"log"
	"sync"
	"time"
)

// QueryLogCleaner는 보관주기가 지난 쿼리 로그를 주기적으로 삭제합니다
type QueryLogCleaner struct {
	deleter       queryLogDeleter
	retentionDays int

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

type queryLogDeleter interface {
	DeleteBefore(cutoff time.Time) (int64, error)
}

// NewQueryLogCleaner는 새로운 QueryLogCleaner를 생성합니다
func NewQueryLogCleaner(deleter queryLogDeleter, retentionDays int) *QueryLogCleaner {
	if deleter == nil {
		return nil
	}
	if retentionDays <= 0 {
		retentionDays = 7
	}

	c := &QueryLogCleaner{
		deleter:       deleter,
		retentionDays: retentionDays,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	go c.run()

	return c
}

// Stop은 클리너를 종료합니다
func (c *QueryLogCleaner) Stop() {
	if c == nil {
		return
	}
	c.stopOnce.Do(func() {
		close(c.stopCh)
		<-c.doneCh
	})
}

func (c *QueryLogCleaner) run() {
	defer close(c.doneCh)

	// 시작 시 1회 실행
	c.cleanup()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.cleanup()
		case <-c.stopCh:
			return
		}
	}
}

func (c *QueryLogCleaner) cleanup() {
	cutoff := time.Now().UTC().AddDate(0, 0, -c.retentionDays)
	deleted, err := c.deleter.DeleteBefore(cutoff)
	if err != nil {
		log.Printf("[QueryLog] retention cleanup 실패: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("[QueryLog] retention cleanup: %d개 항목 정리 (기준: %v)", deleted, cutoff.Format(time.RFC3339))
	}
}
