package gslb

import (
	"dns-go/model"
	"dns-go/storage"
	"fmt"
	"net"
	"os"
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

// TestFallbackWhenPoolEmpty는 매칭된 풀에 멤버가 없을 때 fallback 풀로 전환되는지 테스트
func TestFallbackWhenPoolEmpty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "empty-pool",
		Domain:     "empty.gslb.yangs.sh.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// 매칭되는 풀 (멤버 없음)
	_, err = poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "empty-cidr",
		MatchType:    "cidr",
		MatchValue:   "10.97.0.0/16",
		Priority:     10,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create empty pool error: %v", err)
	}

	// Fallback 풀 (멤버 있음)
	fallbackPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fallback",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     100,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackPoolID,
		Address: "10.96.50.21",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create fallback member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)

	// 빈 풀 매칭 → fallback으로 전환
	ips, ttl, err := engine.Resolve("empty.gslb.yangs.sh.", "A", net.ParseIP("10.97.12.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 60 {
		t.Fatalf("expected ttl 60, got %d", ttl)
	}
	if len(ips) != 1 {
		t.Fatalf("expected 1 ip from fallback, got %d", len(ips))
	}
	if ips[0].String() != "10.96.50.21" {
		t.Fatalf("expected fallback IP 10.96.50.21, got %s", ips[0].String())
	}
}

// TestFallbackWhenAllMembersDisabled는 매칭된 풀의 모든 멤버가 disabled일 때 fallback 풀로 전환되는지 테스트
func TestFallbackWhenAllMembersDisabled(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "disabled-pool",
		Domain:     "disabled.gslb.yangs.sh.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// 매칭되는 풀 (모든 멤버 disabled)
	disabledPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "all-disabled",
		MatchType:    "cidr",
		MatchValue:   "10.97.0.0/16",
		Priority:     10,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  disabledPoolID,
		Address: "10.97.11.18",
		Weight:  100,
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("create disabled member1 error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  disabledPoolID,
		Address: "10.97.11.19",
		Weight:  100,
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("create disabled member2 error: %v", err)
	}

	// Fallback 풀 (정상 멤버)
	fallbackPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fallback",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     100,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackPoolID,
		Address: "10.96.50.21",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create fallback member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)

	// 모든 멤버 disabled → fallback 전환
	ips, ttl, err := engine.Resolve("disabled.gslb.yangs.sh.", "A", net.ParseIP("10.97.12.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 60 {
		t.Fatalf("expected ttl 60, got %d", ttl)
	}
	if len(ips) != 1 {
		t.Fatalf("expected 1 ip from fallback, got %d", len(ips))
	}
	if ips[0].String() != "10.96.50.21" {
		t.Fatalf("expected fallback IP 10.96.50.21, got %s", ips[0].String())
	}
}

// TestFallbackEmptyPoolNoFallback는 빈 풀이고 fallback도 없을 때 빈 응답 반환 (RFC 8020 NXDOMAIN 아닌 NOERROR)
func TestFallbackEmptyPoolNoFallback(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "no-fallback",
		Domain:     "nofb.gslb.yangs.sh.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// 매칭되는 풀 (멤버 없음, fallback도 없음)
	_, err = poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "empty",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     10,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)

	ips, _, err := engine.Resolve("nofb.gslb.yangs.sh.", "A", net.ParseIP("10.97.12.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 0 {
		t.Fatalf("expected no ips for empty pool without fallback, got %v", ips)
	}
}

// TestFallbackEmptyPoolWithUnhealthyFallback는 빈 풀이고 fallback 풀의 멤버도 모두 unhealthy일 때
// fallback 풀의 전체 멤버를 반환하는지 테스트 (최후 수단)
func TestFallbackEmptyPoolWithUnhealthyFallback(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "empty-unhealthy-fb",
		Domain:     "emptyfb.gslb.yangs.sh.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// 빈 매칭 풀
	_, err = poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "empty-cidr",
		MatchType:    "cidr",
		MatchValue:   "10.97.0.0/16",
		Priority:     10,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create empty pool error: %v", err)
	}

	// Fallback 풀 (멤버는 있지만 모두 unhealthy)
	fallbackPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fallback",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     100,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}

	fb1ID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackPoolID,
		Address: "10.96.50.21",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create fallback member1 error: %v", err)
	}

	fb2ID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackPoolID,
		Address: "10.96.50.22",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create fallback member2 error: %v", err)
	}

	healthStatus := &sync.Map{}
	healthStatus.Store(fb1ID, HealthStatus{Healthy: false})
	healthStatus.Store(fb2ID, HealthStatus{Healthy: false})

	engine := NewEngine(policyStorage, poolStorage, nil, healthStatus)

	// 빈 풀 → fallback도 unhealthy → fallback 전체 멤버 반환 (최후 수단)
	ips, ttl, err := engine.Resolve("emptyfb.gslb.yangs.sh.", "A", net.ParseIP("10.97.12.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 60 {
		t.Fatalf("expected ttl 60, got %d", ttl)
	}
	if len(ips) != 2 {
		t.Fatalf("expected 2 ips (all fallback members), got %d", len(ips))
	}

	expectedIPs := map[string]bool{"10.96.50.21": true, "10.96.50.22": true}
	for _, ip := range ips {
		if !expectedIPs[ip.String()] {
			t.Fatalf("unexpected ip: %s", ip.String())
		}
	}
}

// TestFallbackMultipleEmptyPoolScenarios는 다양한 빈 풀 시나리오를 종합 테스트
func TestFallbackMultipleEmptyPoolScenarios(t *testing.T) {
	scenarios := []struct {
		name           string
		primaryMembers []struct {
			address string
			enabled bool
		}
		fallbackMembers []struct {
			address string
			enabled bool
		}
		healthOverrides map[string]bool // address -> healthy
		expectedIPs     []string
		expectEmpty     bool
	}{
		{
			name:           "primary empty, fallback has healthy members",
			primaryMembers: nil,
			fallbackMembers: []struct {
				address string
				enabled bool
			}{
				{"203.0.113.1", true},
			},
			expectedIPs: []string{"203.0.113.1"},
		},
		{
			name: "primary all disabled, fallback has healthy members",
			primaryMembers: []struct {
				address string
				enabled bool
			}{
				{"10.0.0.1", false},
				{"10.0.0.2", false},
			},
			fallbackMembers: []struct {
				address string
				enabled bool
			}{
				{"203.0.113.1", true},
			},
			expectedIPs: []string{"203.0.113.1"},
		},
		{
			name:            "primary empty, fallback also empty",
			primaryMembers:  nil,
			fallbackMembers: nil,
			expectEmpty:     true,
		},
		{
			name: "primary all disabled, fallback all disabled",
			primaryMembers: []struct {
				address string
				enabled bool
			}{
				{"10.0.0.1", false},
			},
			fallbackMembers: []struct {
				address string
				enabled bool
			}{
				{"203.0.113.1", false},
			},
			expectEmpty: true,
		},
	}

	for i, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			path := fmt.Sprintf("/tmp/test_gslb_empty_pool_%d.db", i)
			db, err := storage.NewDatabase(path)
			if err != nil {
				t.Fatalf("db init error: %v", err)
			}
			defer func() {
				_ = db.Close()
				_ = os.Remove(path)
			}()

			policyStorage := NewPolicyStorage(db)
			poolStorage := NewPoolStorage(db)

			policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
				Name:       fmt.Sprintf("scenario-%d", i),
				Domain:     fmt.Sprintf("s%d.gslb.yangs.sh.", i),
				RecordType: "A",
				TTL:        60,
				Enabled:    true,
			})
			if err != nil {
				t.Fatalf("create policy error: %v", err)
			}

			primaryPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
				PolicyID:     policyID,
				Name:         "primary",
				MatchType:    "cidr",
				MatchValue:   "10.97.0.0/16",
				Priority:     10,
				FallbackPool: false,
			})
			if err != nil {
				t.Fatalf("create primary pool error: %v", err)
			}

			for _, m := range sc.primaryMembers {
				_, err = poolStorage.CreateMember(&model.GSLBMember{
					PoolID:  primaryPoolID,
					Address: m.address,
					Weight:  100,
					Enabled: m.enabled,
				})
				if err != nil {
					t.Fatalf("create primary member error: %v", err)
				}
			}

			fallbackPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
				PolicyID:     policyID,
				Name:         "fallback",
				MatchType:    "default",
				MatchValue:   "*",
				Priority:     100,
				FallbackPool: true,
			})
			if err != nil {
				t.Fatalf("create fallback pool error: %v", err)
			}

			healthStatus := &sync.Map{}
			for _, m := range sc.fallbackMembers {
				memberID, err := poolStorage.CreateMember(&model.GSLBMember{
					PoolID:  fallbackPoolID,
					Address: m.address,
					Weight:  100,
					Enabled: m.enabled,
				})
				if err != nil {
					t.Fatalf("create fallback member error: %v", err)
				}
				if sc.healthOverrides != nil {
					if healthy, ok := sc.healthOverrides[m.address]; ok {
						healthStatus.Store(memberID, HealthStatus{Healthy: healthy})
					}
				}
			}

			engine := NewEngine(policyStorage, poolStorage, nil, healthStatus)

			ips, _, err := engine.Resolve(fmt.Sprintf("s%d.gslb.yangs.sh.", i), "A", net.ParseIP("10.97.12.1"))
			if err != nil {
				t.Fatalf("resolve error: %v", err)
			}

			if sc.expectEmpty {
				if len(ips) != 0 {
					t.Fatalf("expected no ips, got %v", ips)
				}
				return
			}

			if len(ips) != len(sc.expectedIPs) {
				t.Fatalf("expected %d ips, got %d: %v", len(sc.expectedIPs), len(ips), ips)
			}

			expectedSet := make(map[string]bool)
			for _, ip := range sc.expectedIPs {
				expectedSet[ip] = true
			}
			for _, ip := range ips {
				if !expectedSet[ip.String()] {
					t.Fatalf("unexpected ip: %s", ip.String())
				}
			}
		})
	}
}
