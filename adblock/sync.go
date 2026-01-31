package adblock

import (
	"database/sql"
	"dns-go/model"
	"dns-go/storage"
	"log"
	"sync"
	"time"
)

type Syncer struct {
	storage  *storage.AdblockStorage
	loader   *Loader
	filter   *Filter
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

func NewSyncer(storage *storage.AdblockStorage, loader *Loader, filter *Filter, interval time.Duration) *Syncer {
	return &Syncer{
		storage:  storage,
		loader:   loader,
		filter:   filter,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
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
		return err
	}
	for _, src := range sources {
		if err := s.syncSource(src); err != nil {
			log.Printf("[Adblock] source sync failed (%s): %v", src.Name, err)
		}
	}
	return s.filter.Rebuild()
}

func (s *Syncer) SyncSource(id int64) error {
	src, err := s.storage.GetAdblockSource(id)
	if err != nil {
		return err
	}
	if src == nil {
		return nil
	}
	if !src.Enabled {
		return nil
	}
	if err := s.syncSource(src); err != nil {
		return err
	}
	return s.filter.Rebuild()
}

func (s *Syncer) syncSource(src *model.AdblockSource) error {
	lastMod := ""
	if src.LastModified.Valid {
		lastMod = src.LastModified.String
	}
	rules, lastModified, err := s.loader.Download(src.URL, lastMod)
	if err != nil {
		return err
	}

	if rules != nil {
		if err := s.storage.RemoveBlockedDomains(src.ID); err != nil {
			return err
		}
		for _, domain := range rules {
			if err := s.storage.AddBlockedDomain(src.ID, domain); err != nil {
				return err
			}
		}
		src.RuleCount = int64(len(rules))
	}

	src.LastModified = sql.NullString{String: lastModified, Valid: lastModified != ""}
	src.LastSync = sql.NullTime{Time: time.Now(), Valid: true}
	return s.storage.UpdateAdblockSource(src)
}
