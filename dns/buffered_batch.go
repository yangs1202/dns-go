package dns

import (
	"sync"
	"time"
)

const (
	defaultBatchFlushInterval = 2 * time.Second
	defaultBatchBufferSize    = 1000
)

type batchFlushFunc[T any] func([]T) error
type batchErrorFunc func(count int, err error)

type bufferedBatch[T any] struct {
	flushBatch batchFlushFunc[T]
	onError    batchErrorFunc

	flushInterval time.Duration
	bufferSize    int

	mu      sync.Mutex
	pending []T

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

func newBufferedBatch[T any](
	flushInterval time.Duration,
	bufferSize int,
	flushBatch batchFlushFunc[T],
	onError batchErrorFunc,
) *bufferedBatch[T] {
	if flushInterval <= 0 {
		flushInterval = defaultBatchFlushInterval
	}
	if bufferSize <= 0 {
		bufferSize = defaultBatchBufferSize
	}

	b := &bufferedBatch[T]{
		flushBatch:    flushBatch,
		onError:       onError,
		flushInterval: flushInterval,
		bufferSize:    bufferSize,
		pending:       make([]T, 0, bufferSize),
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	go b.run()

	return b
}

func (b *bufferedBatch[T]) Add(entry T) bool {
	if b == nil {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.pending) >= b.bufferSize {
		return false
	}
	b.pending = append(b.pending, entry)
	return true
}

func (b *bufferedBatch[T]) Stop() {
	if b == nil {
		return
	}
	b.stopOnce.Do(func() {
		close(b.stopCh)
		<-b.doneCh
	})
}

func (b *bufferedBatch[T]) run() {
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()
	defer close(b.doneCh)

	for {
		select {
		case <-ticker.C:
			b.flush()
		case <-b.stopCh:
			b.flush()
			return
		}
	}
}

func (b *bufferedBatch[T]) flush() {
	pending := b.takePending()
	if len(pending) == 0 {
		return
	}

	if err := b.flushBatch(pending); err != nil && b.onError != nil {
		b.onError(len(pending), err)
	}
}

func (b *bufferedBatch[T]) takePending() []T {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.pending) == 0 {
		return nil
	}

	batch := b.pending
	b.pending = make([]T, 0, b.bufferSize)
	return batch
}
