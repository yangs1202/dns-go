package dns

import (
	"dns-go/model"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type queryLogBatchWriterFunc struct {
	mu      sync.Mutex
	batches [][]*model.QueryLog
	err     error
}

func (w *queryLogBatchWriterFunc) BatchInsert(logs []*model.QueryLog) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.batches = append(w.batches, logs)
	return w.err
}

func (w *queryLogBatchWriterFunc) batchCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.batches)
}

func TestNewQueryLogWriterDefaultsAndNilWriter(t *testing.T) {
	assert.Nil(t, NewQueryLogWriter(nil, time.Second, 10))

	fake := &queryLogBatchWriterFunc{}
	writer := NewQueryLogWriter(fake, 0, 0)
	require.NotNil(t, writer)
	defer writer.Stop()

	assert.Equal(t, 2*time.Second, writer.flushInterval)
	assert.Equal(t, 1000, writer.bufferSize)
}

func TestQueryLogWriterRecordFlushAndDropWhenFull(t *testing.T) {
	fake := &queryLogBatchWriterFunc{}
	writer := NewQueryLogWriter(fake, time.Hour, 2)
	require.NotNil(t, writer)
	defer writer.Stop()

	writer.Record(nil)
	writer.Record(&model.QueryLog{Domain: "one.example."})
	writer.Record(&model.QueryLog{Domain: "two.example."})
	writer.Record(&model.QueryLog{Domain: "dropped.example."})

	pending := writer.takePending()
	require.Len(t, pending, 2)
	assert.Equal(t, "one.example.", pending[0].Domain)
	assert.Equal(t, "two.example.", pending[1].Domain)
	assert.Nil(t, writer.takePending())

	writer.Record(&model.QueryLog{Domain: "flush.example."})
	writer.flush()
	assert.Equal(t, 1, fake.batchCount())
}

func TestQueryLogWriterFlushKeepsRunningOnInsertError(t *testing.T) {
	fake := &queryLogBatchWriterFunc{err: errors.New("insert failed")}
	writer := NewQueryLogWriter(fake, time.Hour, 10)
	require.NotNil(t, writer)
	defer writer.Stop()

	writer.Record(&model.QueryLog{Domain: "error.example."})
	writer.flush()
	assert.Equal(t, 1, fake.batchCount())
	assert.Nil(t, writer.takePending())
}

func TestQueryLogWriterStopFlushesPending(t *testing.T) {
	fake := &queryLogBatchWriterFunc{}
	writer := NewQueryLogWriter(fake, time.Hour, 10)
	require.NotNil(t, writer)

	writer.Record(&model.QueryLog{Domain: "stop.example."})
	writer.Stop()
	writer.Stop()

	assert.Equal(t, 1, fake.batchCount())
}
