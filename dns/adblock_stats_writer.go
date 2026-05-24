package dns

import (
	"dns-go/storage"
	"log"
	"sync"
	"time"
)

type AdblockStatsBatchWriter interface {
	BatchRecordBlockedQueries(records []storage.BlockedQueryRecord) error
}

type AdblockStatsWriter struct {
	writer        AdblockStatsBatchWriter
	flushInterval time.Duration
	bufferSize    int

	mu      sync.Mutex
	pending []storage.BlockedQueryRecord

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

func NewAdblockStatsWriter(writer AdblockStatsBatchWriter, flushInterval time.Duration, bufferSize int) *AdblockStatsWriter {
	if writer == nil {
		return nil
	}
	if flushInterval <= 0 {
		flushInterval = 2 * time.Second
	}
	if bufferSize <= 0 {
		bufferSize = 1000
	}

	w := &AdblockStatsWriter{
		writer:        writer,
		flushInterval: flushInterval,
		bufferSize:    bufferSize,
		pending:       make([]storage.BlockedQueryRecord, 0, bufferSize),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	go w.run()

	return w
}

func (w *AdblockStatsWriter) Record(domain, clientIP string) {
	if w == nil || domain == "" || clientIP == "" {
		return
	}

	w.mu.Lock()
	if len(w.pending) >= w.bufferSize {
		w.mu.Unlock()
		return
	}
	w.pending = append(w.pending, storage.BlockedQueryRecord{
		Domain:   domain,
		ClientIP: clientIP,
	})
	w.mu.Unlock()
}

func (w *AdblockStatsWriter) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.stopCh)
		<-w.doneCh
	})
}

func (w *AdblockStatsWriter) run() {
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

func (w *AdblockStatsWriter) flush() {
	pending := w.takePending()
	if len(pending) == 0 {
		return
	}

	if err := w.writer.BatchRecordBlockedQueries(pending); err != nil {
		log.Printf("[Adblock] stats flush 실패 (%d건): %v", len(pending), err)
	}
}

func (w *AdblockStatsWriter) takePending() []storage.BlockedQueryRecord {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.pending) == 0 {
		return nil
	}

	batch := w.pending
	w.pending = make([]storage.BlockedQueryRecord, 0, w.bufferSize)
	return batch
}
