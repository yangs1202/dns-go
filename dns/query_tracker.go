package dns

import (
	"log"
	"sync"
	"time"
)

const defaultLastQueryFlushInterval = 5 * time.Second

type lastQueryBatchWriter interface {
	BatchUpdateLastQueryAt(lastQueries map[string]time.Time) error
}

// lastQueryTracker는 domain별 마지막 조회 시각을 메모리에 모아 배치로 반영합니다.
type lastQueryTracker struct {
	writer        lastQueryBatchWriter
	flushInterval time.Duration

	mu      sync.Mutex
	pending map[string]time.Time

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

func newLastQueryTracker(writer lastQueryBatchWriter, flushInterval time.Duration) *lastQueryTracker {
	if writer == nil {
		return nil
	}
	if flushInterval <= 0 {
		flushInterval = defaultLastQueryFlushInterval
	}

	t := &lastQueryTracker{
		writer:        writer,
		flushInterval: flushInterval,
		pending:       make(map[string]time.Time),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	go t.run()

	return t
}

func (t *lastQueryTracker) Record(domain string, queriedAt time.Time) {
	if t == nil || domain == "" {
		return
	}

	queriedAt = queriedAt.UTC()

	t.mu.Lock()
	if prev, ok := t.pending[domain]; !ok || queriedAt.After(prev) {
		t.pending[domain] = queriedAt
	}
	t.mu.Unlock()
}

func (t *lastQueryTracker) Stop() {
	if t == nil {
		return
	}

	t.stopOnce.Do(func() {
		close(t.stopCh)
		<-t.doneCh
	})
}

func (t *lastQueryTracker) run() {
	ticker := time.NewTicker(t.flushInterval)
	defer ticker.Stop()
	defer close(t.doneCh)

	for {
		select {
		case <-ticker.C:
			t.flush()
		case <-t.stopCh:
			t.flush()
			return
		}
	}
}

func (t *lastQueryTracker) flush() {
	pending := t.takePending()
	if len(pending) == 0 {
		return
	}

	if err := t.writer.BatchUpdateLastQueryAt(pending); err != nil {
		log.Printf("[DNS] last_query_at flush 실패: %v", err)
		t.requeue(pending)
	}
}

func (t *lastQueryTracker) takePending() map[string]time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.pending) == 0 {
		return nil
	}

	batch := t.pending
	t.pending = make(map[string]time.Time)
	return batch
}

func (t *lastQueryTracker) requeue(batch map[string]time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for domain, queriedAt := range batch {
		if prev, ok := t.pending[domain]; !ok || queriedAt.After(prev) {
			t.pending[domain] = queriedAt
		}
	}
}
