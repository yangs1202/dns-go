package gslb

import (
	"crypto/tls"
	"dns-go/model"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type HealthCheckWorker struct {
	storage       *HealthCheckStorage
	memberStorage *PoolStorage
	healthStatus  *sync.Map
	stopCh        chan struct{}
	wg            sync.WaitGroup
}

func NewHealthCheckWorker(storage *HealthCheckStorage, memberStorage *PoolStorage, healthStatus *sync.Map) *HealthCheckWorker {
	return &HealthCheckWorker{
		storage:       storage,
		memberStorage: memberStorage,
		healthStatus:  healthStatus,
		stopCh:        make(chan struct{}),
	}
}

func (w *HealthCheckWorker) Start() {
	checks, err := w.storage.ListHealthChecks()
	if err != nil {
		log.Printf("헬스체크 목록 조회 실패: %v", err)
		return
	}
	log.Printf("헬스체크 시작: %d개 체크", len(checks))

	for _, check := range checks {
		if !check.Enabled {
			continue
		}
		w.wg.Add(1)
		go func(c *model.HealthCheck) {
			defer w.wg.Done()
			w.runCheckLoop(c)
		}(check)
	}
}

func (w *HealthCheckWorker) Stop() {
	close(w.stopCh)
	w.wg.Wait()
}

func (w *HealthCheckWorker) runCheckLoop(check *model.HealthCheck) {
	ticker := time.NewTicker(time.Duration(check.IntervalSec) * time.Second)
	defer ticker.Stop()

	member, _ := w.memberStorage.GetMember(check.MemberID)
	if member != nil {
		w.runCheck(check, member)
	}

	for {
		select {
		case <-ticker.C:
			member, _ := w.memberStorage.GetMember(check.MemberID)
			if member != nil {
				w.runCheck(check, member)
			}
		case <-w.stopCh:
			return
		}
	}
}

func (w *HealthCheckWorker) runCheck(check *model.HealthCheck, member *model.GSLBMember) {
	err := w.probe(check, member)
	status := w.getStatus(member.ID)
	status.LastCheck = time.Now()

	if err == nil {
		status.ConsecutiveOKs++
		status.ConsecutiveFails = 0
		status.LastError = ""
		if status.ConsecutiveOKs >= int(check.HealthyThreshold) {
			status.Healthy = true
		}
	} else {
		status.ConsecutiveFails++
		status.ConsecutiveOKs = 0
		status.LastError = err.Error()
		if status.ConsecutiveFails >= int(check.UnhealthyThreshold) {
			status.Healthy = false
		}
	}

	w.healthStatus.Store(member.ID, status)
}

func (w *HealthCheckWorker) probe(check *model.HealthCheck, member *model.GSLBMember) error {
	switch check.CheckType {
	case "http":
		// Target URL의 scheme을 자동 감지 (http:// 또는 https://)
		client := &http.Client{
			Timeout: time.Duration(check.TimeoutSec) * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
		resp, err := client.Get(check.Target)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("http status %d", resp.StatusCode)
		}
		return nil
	case "tcp":
		conn, err := net.DialTimeout("tcp", check.Target, time.Duration(check.TimeoutSec)*time.Second)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	default:
		// 기본값: TCP 체크
		conn, err := net.DialTimeout("tcp", check.Target, time.Duration(check.TimeoutSec)*time.Second)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	}
}

func (w *HealthCheckWorker) getStatus(memberID int64) HealthStatus {
	if v, ok := w.healthStatus.Load(memberID); ok {
		if status, ok := v.(HealthStatus); ok {
			return status
		}
	}
	return HealthStatus{Healthy: true}
}
