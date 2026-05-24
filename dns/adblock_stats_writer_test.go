package dns

import (
	"dns-go/storage"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type adblockStatsBatchWriterFunc struct {
	mu      sync.Mutex
	batches [][]storage.BlockedQueryRecord
	err     error
}

func (w *adblockStatsBatchWriterFunc) BatchRecordBlockedQueries(records []storage.BlockedQueryRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.batches = append(w.batches, records)
	return w.err
}

func (w *adblockStatsBatchWriterFunc) batchCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.batches)
}

func (w *adblockStatsBatchWriterFunc) recordCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	count := 0
	for _, batch := range w.batches {
		count += len(batch)
	}
	return count
}

func TestNewAdblockStatsWriterDefaultsAndNilWriter(t *testing.T) {
	assert.Nil(t, NewAdblockStatsWriter(nil, time.Second, 10))

	fake := &adblockStatsBatchWriterFunc{}
	writer := NewAdblockStatsWriter(fake, 0, 0)
	require.NotNil(t, writer)
	defer writer.Stop()

	assert.Equal(t, 2*time.Second, writer.flushInterval)
	assert.Equal(t, 1000, writer.bufferSize)
}

func TestAdblockStatsWriterNilReceiverIsNoop(t *testing.T) {
	var writer *AdblockStatsWriter
	writer.Record("noop.example.", "192.0.2.1")
	writer.Stop()
}

func TestAdblockStatsWriterRecordFlushAndDropWhenFull(t *testing.T) {
	fake := &adblockStatsBatchWriterFunc{}
	writer := NewAdblockStatsWriter(fake, time.Hour, 2)
	require.NotNil(t, writer)
	defer writer.Stop()

	writer.Record("", "192.0.2.1")
	writer.Record("one.example.", "")
	writer.Record("one.example.", "192.0.2.1")
	writer.Record("two.example.", "192.0.2.2")
	writer.Record("dropped.example.", "192.0.2.3")

	pending := writer.takePending()
	require.Len(t, pending, 2)
	assert.Equal(t, "one.example.", pending[0].Domain)
	assert.Equal(t, "two.example.", pending[1].Domain)
	assert.Nil(t, writer.takePending())

	writer.Record("flush.example.", "192.0.2.4")
	writer.flush()
	assert.Equal(t, 1, fake.batchCount())
}

func TestAdblockStatsWriterTickerFlushesPending(t *testing.T) {
	fake := &adblockStatsBatchWriterFunc{}
	writer := NewAdblockStatsWriter(fake, 10*time.Millisecond, 10)
	require.NotNil(t, writer)
	defer writer.Stop()

	writer.Record("ticker.example.", "192.0.2.1")

	require.Eventually(t, func() bool {
		return fake.batchCount() == 1 && fake.recordCount() == 1
	}, time.Second, 10*time.Millisecond)
}

func TestAdblockStatsWriterConcurrentRecordHonorsBufferLimit(t *testing.T) {
	fake := &adblockStatsBatchWriterFunc{}
	writer := NewAdblockStatsWriter(fake, time.Hour, 25)
	require.NotNil(t, writer)
	defer writer.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			writer.Record(fmt.Sprintf("blocked-%d.example.", i), "192.0.2.1")
		}(i)
	}
	wg.Wait()

	pending := writer.takePending()
	assert.Len(t, pending, 25)
	assert.Nil(t, writer.takePending())
}

func TestAdblockStatsWriterFlushKeepsRunningOnInsertError(t *testing.T) {
	fake := &adblockStatsBatchWriterFunc{err: errors.New("insert failed")}
	writer := NewAdblockStatsWriter(fake, time.Hour, 10)
	require.NotNil(t, writer)
	defer writer.Stop()

	writer.Record("error.example.", "192.0.2.1")
	writer.flush()
	assert.Equal(t, 1, fake.batchCount())
	assert.Nil(t, writer.takePending())
}

func TestAdblockStatsWriterStopFlushesPending(t *testing.T) {
	fake := &adblockStatsBatchWriterFunc{}
	writer := NewAdblockStatsWriter(fake, time.Hour, 10)
	require.NotNil(t, writer)

	writer.Record("stop.example.", "192.0.2.1")
	writer.Stop()
	writer.Stop()

	assert.Equal(t, 1, fake.batchCount())
}
