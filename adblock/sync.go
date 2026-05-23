package adblock

import (
	"database/sql"
	"dns-go/metrics"
	"dns-go/model"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"
)

// SyncStorageInterface defines the methods needed from storage for syncing
type SyncStorageInterface interface {
	GetEnabledAdblockSources() ([]*model.AdblockSource, error)
	GetAdblockSource(id int64) (*model.AdblockSource, error)
	UpdateAdblockSource(source *model.AdblockSource) error
	AddBlockedDomain(sourceID int64, domain string) error
	AddBlockedDomainsBatch(sourceID int64, domains []string) error
	RemoveBlockedDomains(sourceID int64) error
}

// LoaderInterface defines the methods for downloading and parsing rules
type LoaderInterface interface {
	Download(url, lastModified string) ([]string, string, error)
}

// FilterInterface defines the methods for filter rebuilding
type FilterInterface interface {
	Rebuild() error
}

// VersionIncrementer는 데이터 변경 시 동기화 버전을 증가시킵니다
type VersionIncrementer interface {
	IncrementVersion(tx *sql.Tx) error
}

type Syncer struct {
	storage            SyncStorageInterface
	loader             LoaderInterface
	filter             FilterInterface
	versionIncrementer VersionIncrementer
	interval           time.Duration
	stopCh             chan struct{}
	wg                 sync.WaitGroup
}

func NewSyncer(storage SyncStorageInterface, loader LoaderInterface, filter FilterInterface, interval time.Duration) *Syncer {
	return &Syncer{
		storage:  storage,
		loader:   loader,
		filter:   filter,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// SetVersionIncrementer는 동기화 버전 증가기를 설정합니다 (Primary 모드에서 사용)
func (s *Syncer) SetVersionIncrementer(v VersionIncrementer) {
	s.versionIncrementer = v
}

func (s *Syncer) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := s.SyncAll(); err != nil {
					log.Printf("[Adblock] sync error: %v", err)
				}
			case <-s.stopCh:
				return
			}
		}
	}()
}

func (s *Syncer) Stop() {
	close(s.stopCh)
	s.wg.Wait()
}

func (s *Syncer) SyncAll() error {
	sources, err := s.storage.GetEnabledAdblockSources()
	if err != nil {
		return fmt.Errorf("enabled adblock sources 조회 실패: %w", err)
	}
	changed := false
	for _, src := range sources {
		updated, err := s.syncSource(src)
		if err != nil {
			log.Printf("[Adblock] source sync failed (%s): %v", src.Name, err)
			continue
		}
		if updated {
			changed = true
		}
	}
	if changed && s.versionIncrementer != nil {
		if err := s.versionIncrementer.IncrementVersion(nil); err != nil {
			log.Printf("[Adblock] version increment failed: %v", err)
		}
	}
	return s.filter.Rebuild()
}

func (s *Syncer) SyncSource(id int64) error {
	src, err := s.storage.GetAdblockSource(id)
	if err != nil {
		return fmt.Errorf("adblock source 조회 실패 (id=%d): %w", id, err)
	}
	if src == nil {
		return nil
	}
	if !src.Enabled {
		return nil
	}
	updated, err := s.syncSource(src)
	if err != nil {
		return fmt.Errorf("adblock source 동기화 실패 (id=%d, name=%s): %w", src.ID, src.Name, err)
	}
	if updated && s.versionIncrementer != nil {
		if err := s.versionIncrementer.IncrementVersion(nil); err != nil {
			log.Printf("[Adblock] version increment failed: %v", err)
		}
	}
	return s.filter.Rebuild()
}

func (s *Syncer) syncSource(src *model.AdblockSource) (bool, error) {
	lastMod := ""
	if src.LastModified.Valid {
		lastMod = src.LastModified.String
	}
	rules, lastModified, err := s.loader.Download(src.URL, lastMod)
	if err != nil {
		return false, fmt.Errorf("rule 다운로드 실패 (source_id=%d, name=%s): %w", src.ID, src.Name, err)
	}

	updated := false
	if rules != nil {
		if err := s.storage.RemoveBlockedDomains(src.ID); err != nil {
			return false, fmt.Errorf("기존 차단 도메인 제거 실패 (source_id=%d): %w", src.ID, err)
		}
		if err := s.storage.AddBlockedDomainsBatch(src.ID, rules); err != nil {
			return false, fmt.Errorf("차단 도메인 배치 추가 실패 (source_id=%d, rules=%d): %w", src.ID, len(rules), err)
		}
		src.RuleCount = int64(len(rules))
		updated = true
	}

	src.LastModified = sql.NullString{String: lastModified, Valid: lastModified != ""}
	src.LastSync = sql.NullTime{Time: time.Now(), Valid: true}

	srcID := strconv.FormatInt(src.ID, 10)
	metrics.AdblockSourceRules.WithLabelValues(srcID, src.Name).Set(float64(src.RuleCount))
	metrics.AdblockSourceLastSync.WithLabelValues(srcID, src.Name).SetToCurrentTime()
	metrics.AdblockLastSyncTimestamp.SetToCurrentTime()

	if err := s.storage.UpdateAdblockSource(src); err != nil {
		return false, fmt.Errorf("adblock source 업데이트 실패 (source_id=%d): %w", src.ID, err)
	}
	return updated, nil
}
