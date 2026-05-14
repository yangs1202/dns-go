package dns

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLastQueryWriter struct {
	mu       sync.Mutex
	calls    int
	lastData map[string]time.Time
	fail     bool
}

func (m *mockLastQueryWriter) BatchUpdateLastQueryAt(lastQueries map[string]time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.fail {
		return errors.New("forced error")
	}

	m.calls++
	m.lastData = make(map[string]time.Time, len(lastQueries))
	for domain, queriedAt := range lastQueries {
		m.lastData[domain] = queriedAt
	}
	return nil
}

func TestLastQueryTracker_FlushLatestPerDomain(t *testing.T) {
	writer := &mockLastQueryWriter{}
	tracker := newLastQueryTracker(writer, time.Hour)
	require.NotNil(t, tracker)
	defer tracker.Stop()

	t1 := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(2 * time.Minute)
	t3 := t1.Add(1 * time.Minute)

	tracker.Record("www.example.com.", t1)
	tracker.Record("www.example.com.", t2)
	tracker.Record("api.example.com.", t3)

	tracker.flush()

	writer.mu.Lock()
	defer writer.mu.Unlock()
	assert.Equal(t, 1, writer.calls)
	require.Len(t, writer.lastData, 2)
	assert.Equal(t, t2.Unix(), writer.lastData["www.example.com."].Unix())
	assert.Equal(t, t3.Unix(), writer.lastData["api.example.com."].Unix())
}

func TestLastQueryTracker_DefaultsAndIgnoredRecords(t *testing.T) {
	assert.Nil(t, newLastQueryTracker(nil, time.Hour))

	writer := &mockLastQueryWriter{}
	tracker := newLastQueryTracker(writer, 0)
	require.NotNil(t, tracker)
	defer tracker.Stop()
	assert.Equal(t, defaultLastQueryFlushInterval, tracker.flushInterval)

	t1 := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(-time.Minute)
	tracker.Record("", t1)
	tracker.Record("www.example.com.", t1)
	tracker.Record("www.example.com.", t2)
	(*lastQueryTracker)(nil).Record("ignored.example.", t1)
	(*lastQueryTracker)(nil).Stop()

	pending := tracker.takePending()
	require.Len(t, pending, 1)
	assert.Equal(t, t1.Unix(), pending["www.example.com."].Unix())
	assert.Nil(t, tracker.takePending())
}

func TestLastQueryTracker_RequeueKeepsNewest(t *testing.T) {
	writer := &mockLastQueryWriter{}
	tracker := newLastQueryTracker(writer, time.Hour)
	require.NotNil(t, tracker)
	defer tracker.Stop()

	t1 := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	tracker.Record("www.example.com.", t2)
	tracker.requeue(map[string]time.Time{"www.example.com.": t1, "api.example.com.": t1})

	pending := tracker.takePending()
	require.Len(t, pending, 2)
	assert.Equal(t, t2.Unix(), pending["www.example.com."].Unix())
	assert.Equal(t, t1.Unix(), pending["api.example.com."].Unix())
}

func TestLastQueryTracker_FlushFailureRequeue(t *testing.T) {
	writer := &mockLastQueryWriter{fail: true}
	tracker := newLastQueryTracker(writer, time.Hour)
	require.NotNil(t, tracker)
	defer tracker.Stop()

	t1 := time.Date(2026, 2, 7, 12, 0, 0, 0, time.UTC)
	tracker.Record("www.example.com.", t1)

	tracker.flush()

	tracker.mu.Lock()
	queued, ok := tracker.pending["www.example.com."]
	tracker.mu.Unlock()
	require.True(t, ok)
	assert.Equal(t, t1.Unix(), queued.Unix())

	writer.mu.Lock()
	writer.fail = false
	writer.mu.Unlock()

	tracker.flush()

	writer.mu.Lock()
	defer writer.mu.Unlock()
	assert.Equal(t, 1, writer.calls)
	require.Contains(t, writer.lastData, "www.example.com.")
}

func TestLastQueryTracker_StopFlushesPending(t *testing.T) {
	writer := &mockLastQueryWriter{}
	tracker := newLastQueryTracker(writer, time.Hour)
	require.NotNil(t, tracker)

	t1 := time.Date(2026, 2, 7, 12, 30, 0, 0, time.UTC)
	tracker.Record("www.example.com.", t1)
	tracker.Stop()

	writer.mu.Lock()
	defer writer.mu.Unlock()
	assert.Equal(t, 1, writer.calls)
	require.Contains(t, writer.lastData, "www.example.com.")
}
