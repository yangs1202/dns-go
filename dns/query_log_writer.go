package dns

import (
	"dns-go/model"
	"log"
	"sync"
	"time"
)

// QueryLogBatchWriter는 쿼리 로그 배치 삽입 인터페이스입니다
type QueryLogBatchWriter interface {
	BatchInsert(logs []*model.QueryLog) error
}

// QueryLogWriter는 DNS 쿼리 로그를 버퍼링하여 배치로 DB에 기록합니다
type QueryLogWriter struct {
	writer        QueryLogBatchWriter
	flushInterval time.Duration
	bufferSize    int

	mu      sync.Mutex
	pending []*model.QueryLog

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// NewQueryLogWriter는 새로운 QueryLogWriter를 생성합니다
func NewQueryLogWriter(writer QueryLogBatchWriter, flushInterval time.Duration, bufferSize int) *QueryLogWriter {
	if writer == nil {
		return nil
	}
	if flushInterval <= 0 {
		flushInterval = 2 * time.Second
	}
	if bufferSize <= 0 {
		bufferSize = 1000
	}

	w := &QueryLogWriter{
		writer:        writer,
		flushInterval: flushInterval,
		bufferSize:    bufferSize,
		pending:       make([]*model.QueryLog, 0, bufferSize),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	go w.run()

	return w
}

// Record는 쿼리 로그 항목을 버퍼에 추가합니다
func (w *QueryLogWriter) Record(entry *model.QueryLog) {
	if w == nil || entry == nil {
		return
	}

	w.mu.Lock()
	if len(w.pending) >= w.bufferSize {
		w.mu.Unlock()
		return
	}
	w.pending = append(w.pending, entry)
	w.mu.Unlock()
}

// Stop은 라이터를 종료하고 남은 로그를 플러시합니다
func (w *QueryLogWriter) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.stopCh)
		<-w.doneCh
	})
}

func (w *QueryLogWriter) run() {
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()
	defer close(w.doneCh)

	for {
		select {
		case <-ticker.C:
			w.flush()
		case <-w.stopCh:
			w.flush()
			return
		}
	}
}

func (w *QueryLogWriter) flush() {
	pending := w.takePending()
	if len(pending) == 0 {
		return
	}

	if err := w.writer.BatchInsert(pending); err != nil {
		log.Printf("[QueryLog] flush 실패 (%d건): %v", len(pending), err)
	}
}

func (w *QueryLogWriter) takePending() []*model.QueryLog {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.pending) == 0 {
		return nil
	}

	batch := w.pending
	w.pending = make([]*model.QueryLog, 0, w.bufferSize)
	return batch
}
