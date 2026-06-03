package dns

import (
	"dns-go/storage"
	"log/slog"
	"time"
)

type AdblockStatsBatchWriter interface {
	BatchRecordBlockedQueries(records []storage.BlockedQueryRecord) error
}

type AdblockStatsWriter struct {
	*bufferedBatch[storage.BlockedQueryRecord]
}

func NewAdblockStatsWriter(writer AdblockStatsBatchWriter, flushInterval time.Duration, bufferSize int) *AdblockStatsWriter {
	if writer == nil {
		return nil
	}

	w := &AdblockStatsWriter{}
	w.bufferedBatch = newBufferedBatch(flushInterval, bufferSize, writer.BatchRecordBlockedQueries, func(count int, err error) {
		slog.Error("adblock stats flush failed", "component", "adblock_stats", "count", count, "error", err)
	})

	return w
}

func (w *AdblockStatsWriter) Record(domain, clientIP string) {
	if w == nil || domain == "" || clientIP == "" {
		return
	}

	w.bufferedBatch.add(storage.BlockedQueryRecord{
		Domain:   domain,
		ClientIP: clientIP,
	})
}

func (w *AdblockStatsWriter) Stop() {
	if w == nil {
		return
	}
	w.bufferedBatch.Stop()
}
