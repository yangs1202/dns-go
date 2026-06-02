package dns

import (
	"dns-go/model"
	"log/slog"
	"time"
)

// QueryLogBatchWriter는 쿼리 로그 배치 삽입 인터페이스입니다
type QueryLogBatchWriter interface {
	BatchInsert(logs []*model.QueryLog) error
}

// QueryLogWriter는 DNS 쿼리 로그를 버퍼링하여 배치로 DB에 기록합니다
type QueryLogWriter struct {
	*bufferedBatch[*model.QueryLog]
}

// NewQueryLogWriter는 새로운 QueryLogWriter를 생성합니다
func NewQueryLogWriter(writer QueryLogBatchWriter, flushInterval time.Duration, bufferSize int) *QueryLogWriter {
	if writer == nil {
		return nil
	}

	w := &QueryLogWriter{}
	w.bufferedBatch = newBufferedBatch(flushInterval, bufferSize, writer.BatchInsert, func(count int, err error) {
		slog.Error("query log flush failed", "component", "query_log", "count", count, "error", err)
	})

	return w
}

// Record는 쿼리 로그 항목을 버퍼에 추가합니다
func (w *QueryLogWriter) Record(entry *model.QueryLog) {
	if w == nil || entry == nil {
		return
	}
	w.bufferedBatch.Add(entry)
}

// Stop은 라이터를 종료하고 남은 로그를 플러시합니다
func (w *QueryLogWriter) Stop() {
	if w == nil {
		return
	}
	w.bufferedBatch.Stop()
}
