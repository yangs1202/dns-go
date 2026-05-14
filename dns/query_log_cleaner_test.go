package dns

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type queryLogDeleterFunc struct {
	cutoffs []time.Time
	deleted int64
	err     error
}

func (d *queryLogDeleterFunc) DeleteBefore(cutoff time.Time) (int64, error) {
	d.cutoffs = append(d.cutoffs, cutoff)
	return d.deleted, d.err
}

func TestNewQueryLogCleanerDefaultsAndNilDeleter(t *testing.T) {
	assert.Nil(t, NewQueryLogCleaner(nil, 7))

	deleter := &queryLogDeleterFunc{}
	cleaner := NewQueryLogCleaner(deleter, 0)
	require.NotNil(t, cleaner)
	cleaner.Stop()
	cleaner.Stop()

	assert.Equal(t, 7, cleaner.retentionDays)
	assert.NotEmpty(t, deleter.cutoffs)
}

func TestQueryLogCleanerCleanupSuccessAndError(t *testing.T) {
	deleter := &queryLogDeleterFunc{deleted: 3}
	cleaner := &QueryLogCleaner{deleter: deleter, retentionDays: 2}
	cleaner.cleanup()

	require.Len(t, deleter.cutoffs, 1)
	assert.WithinDuration(t, time.Now().UTC().AddDate(0, 0, -2), deleter.cutoffs[0], 2*time.Second)

	errorDeleter := &queryLogDeleterFunc{err: errors.New("delete failed")}
	cleaner = &QueryLogCleaner{deleter: errorDeleter, retentionDays: 1}
	cleaner.cleanup()
	assert.Len(t, errorDeleter.cutoffs, 1)
}
