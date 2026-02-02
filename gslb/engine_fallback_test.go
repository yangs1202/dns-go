package gslb

import (
	"dns-go/model"
	"net"
	"sync"
	"testing"
)

// TestFallbackWhenPrimaryUnhealthy는 매칭된 풀의 모든 멤버가 unhealthy일 때 fallback 풀로 전환되는지 테스트
func TestFallbackWhenPrimaryUnhealthy(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	// Policy 생성 (lb.gslb.yangs.sh와 유사)
	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "lb",
		Domain:     "lb.gslb.yangs.sh.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// SG Zone Pool (CIDR 매칭, priority 10)
	sgPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "SG Zone",
		MatchType:    "cidr",
		MatchValue:   "10.97.0.0/16",
		Priority:     10,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create sg pool error: %v", err)
	}

	// DEFAULT Pool (fallback, priority 100)
	defaultPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "DEFAULT",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     100,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create default pool error: %v", err)
	}

	// SG Zone 멤버: 10.97.11.18
	sgMemberID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  sgPoolID,
		Address: "10.97.11.18",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create sg member error: %v", err)
	}

	// DEFAULT 멤버: 10.96.50.21
	defaultMemberID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  defaultPoolID,
		Address: "10.96.50.21",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create default member error: %v", err)
	}

	// 헬스 상태 초기화
	healthStatus := &sync.Map{}

	// 테스트 1: 모두 healthy - SG Zone 멤버 응답
	t.Run("All healthy - return SG Zone member", func(t *testing.T) {
		healthStatus.Store(sgMemberID, HealthStatus{Healthy: true})
		healthStatus.Store(defaultMemberID, HealthStatus{Healthy: true})

		engine := NewEngine(policyStorage, poolStorage, nil, healthStatus)

		// 10.97.12.1에서 요청 (SG Zone CIDR 매칭)
		ips, ttl, err := engine.Resolve("lb.gslb.yangs.sh.", "A", net.ParseIP("10.97.12.1"))
		if err != nil {
			t.Fatalf("resolve error: %v", err)
		}
		if ttl != 60 {
			t.Fatalf("expected ttl 60, got %d", ttl)
		}
		if len(ips) != 1 {
			t.Fatalf("expected 1 ip, got %d", len(ips))
		}
		if ips[0].String() != "10.97.11.18" {
			t.Fatalf("expected SG Zone member 10.97.11.18, got %s", ips[0].String())
		}
	})

	// 테스트 2: SG Zone unhealthy - DEFAULT 멤버로 fallback
	t.Run("SG Zone unhealthy - fallback to DEFAULT", func(t *testing.T) {
		healthStatus.Store(sgMemberID, HealthStatus{Healthy: false})
		healthStatus.Store(defaultMemberID, HealthStatus{Healthy: true})

		engine := NewEngine(policyStorage, poolStorage, nil, healthStatus)

		// 10.97.12.1에서 요청 (SG Zone CIDR 매칭되지만 unhealthy)
		ips, ttl, err := engine.Resolve("lb.gslb.yangs.sh.", "A", net.ParseIP("10.97.12.1"))
		if err != nil {
			t.Fatalf("resolve error: %v", err)
		}
		if ttl != 60 {
			t.Fatalf("expected ttl 60, got %d", ttl)
		}
		if len(ips) != 1 {
			t.Fatalf("expected 1 ip (fallback), got %d", len(ips))
		}
		t.Logf("sgMemberID=%d, defaultMemberID=%d, sgPoolID=%d, defaultPoolID=%d", sgMemberID, defaultMemberID, sgPoolID, defaultPoolID)
		t.Logf("returned IP: %s", ips[0].String())
		if ips[0].String() != "10.96.50.21" {
			t.Fatalf("expected fallback to DEFAULT member 10.96.50.21, got %s", ips[0].String())
		}
	})

	// 테스트 3: 모두 unhealthy - 모든 멤버 응답 (최후 수단)
	t.Run("All unhealthy - return all members", func(t *testing.T) {
		healthStatus.Store(sgMemberID, HealthStatus{Healthy: false})
		healthStatus.Store(defaultMemberID, HealthStatus{Healthy: false})

		engine := NewEngine(policyStorage, poolStorage, nil, healthStatus)

		// 10.97.12.1에서 요청
		ips, _, err := engine.Resolve("lb.gslb.yangs.sh.", "A", net.ParseIP("10.97.12.1"))
		if err != nil {
			t.Fatalf("resolve error: %v", err)
		}
		// allFailed이므로 SG Zone의 모든 멤버 반환 (1개)
		if len(ips) != 1 {
			t.Fatalf("expected 1 ip when all failed, got %d", len(ips))
		}
		if ips[0].String() != "10.97.11.18" {
			t.Fatalf("expected 10.97.11.18, got %s", ips[0].String())
		}
	})

	// 테스트 4: 다른 대역에서 요청 - 항상 DEFAULT 풀 사용
	t.Run("Request from other network - use DEFAULT pool", func(t *testing.T) {
		healthStatus.Store(sgMemberID, HealthStatus{Healthy: true})
		healthStatus.Store(defaultMemberID, HealthStatus{Healthy: true})

		engine := NewEngine(policyStorage, poolStorage, nil, healthStatus)

		// 192.168.1.1에서 요청 (SG Zone CIDR 불일치)
		ips, _, err := engine.Resolve("lb.gslb.yangs.sh.", "A", net.ParseIP("192.168.1.1"))
		if err != nil {
			t.Fatalf("resolve error: %v", err)
		}
		if len(ips) != 1 {
			t.Fatalf("expected 1 ip, got %d", len(ips))
		}
		if ips[0].String() != "10.96.50.21" {
			t.Fatalf("expected DEFAULT member 10.96.50.21, got %s", ips[0].String())
		}
	})
}
