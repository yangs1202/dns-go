package gslb

import (
	"errors"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"dns-go/metrics"
	"dns-go/model"
)

type Engine struct {
	policyStorage *PolicyStorage
	poolStorage   *PoolStorage
	geoip         *GeoIPResolver
	healthStatus  *sync.Map
}

func NewEngine(policyStorage *PolicyStorage, poolStorage *PoolStorage, geoip *GeoIPResolver, healthStatus *sync.Map) *Engine {
	return &Engine{
		policyStorage: policyStorage,
		poolStorage:   poolStorage,
		geoip:         geoip,
		healthStatus:  healthStatus,
	}
}

func (e *Engine) Resolve(domain, qtype string, clientIP net.IP) ([]net.IP, uint32, error) {
	if e == nil {
		return nil, 0, nil
	}

	policy, err := e.policyStorage.GetPolicyByDomain(domain, qtype)
	if err != nil {
		return nil, 0, err
	}
	if policy == nil {
		return nil, 0, nil
	}

	pools, err := e.poolStorage.GetPoolsByPolicy(policy.ID)
	if err != nil {
		return nil, 0, err
	}

	var matched *model.GSLBPool
	var fallback *model.GSLBPool
	for _, pool := range pools {
		if pool.FallbackPool {
			fallback = pool
		}
		if matched == nil && e.matchPool(pool, clientIP) {
			matched = pool
		}
	}
	if matched == nil {
		matched = fallback
	}
	if matched == nil {
		return nil, 0, nil
	}

	members, err := e.poolStorage.GetMembersByPool(matched.ID)
	if err != nil {
		return nil, 0, err
	}

	selected, allFailed := e.selectMembers(members)

	// 매칭된 풀에 활성 멤버가 없거나(빈 풀/모두 disabled), 모든 멤버가 unhealthy이면 fallback 시도
	emptyPool := len(selected) == 0 && !allFailed
	if (allFailed || emptyPool) && fallback != nil && fallback.ID != matched.ID {
		fallbackMembers, err := e.poolStorage.GetMembersByPool(fallback.ID)
		if err == nil && len(fallbackMembers) > 0 {
			fallbackSelected, fallbackAllFailed := e.selectMembers(fallbackMembers)
			if len(fallbackSelected) > 0 {
				if !fallbackAllFailed {
					// fallback 풀에 healthy 멤버가 있으면 사용
					selected = fallbackSelected
					allFailed = false
				} else if emptyPool {
					// 원래 풀이 비어있으면 fallback 풀의 전체 멤버라도 사용
					selected = fallbackSelected
					allFailed = true
				}
				// 원래 풀에 멤버가 있고 fallback도 모두 unhealthy면 원래 풀 유지
			}
		}
	}

	if len(selected) == 0 {
		return nil, 0, nil
	}

	// Member.Address는 순수 IP만 포함 (포트 제외)
	// 예: "10.97.11.18" 또는 "2001:db8::1"
	ips := make([]net.IP, 0, len(selected))
	for _, member := range selected {
		ip := net.ParseIP(member.Address)
		if ip == nil {
			return nil, 0, errors.New("멤버 IP 파싱 실패: " + member.Address)
		}
		ips = append(ips, ip)
	}

	metrics.GSLBQueriesTotal.WithLabelValues(strconv.FormatInt(policy.ID, 10), policy.Name).Inc()

	// 모든 멤버가 실패한 경우 전체 응답, 그렇지 않으면 단일 응답
	if allFailed {
		return ips, uint32(policy.TTL), nil
	}
	return ips[:1], uint32(policy.TTL), nil
}

func (e *Engine) matchPool(pool *model.GSLBPool, clientIP net.IP) bool {
	if pool == nil {
		return false
	}
	switch strings.ToLower(pool.MatchType) {
	case "cidr":
		if clientIP == nil {
			return false
		}
		_, cidr, err := net.ParseCIDR(pool.MatchValue)
		if err != nil {
			return false
		}
		return cidr.Contains(clientIP)
	case "geo_country":
		if clientIP == nil || e.geoip == nil {
			return false
		}
		country, _, err := e.geoip.Country(clientIP.String())
		if err != nil {
			return false
		}
		return strings.EqualFold(country, pool.MatchValue)
	case "geo_continent":
		if clientIP == nil || e.geoip == nil {
			return false
		}
		_, continent, err := e.geoip.Country(clientIP.String())
		if err != nil {
			return false
		}
		return strings.EqualFold(continent, pool.MatchValue)
	case "default":
		return true
	default:
		return false
	}
}

// selectMembers는 GSLB 멤버를 선택하고 모든 멤버가 실패했는지 여부를 반환합니다
// 반환값: (선택된 멤버들, 모든 멤버가 실패했는지 여부)
func (e *Engine) selectMembers(members []*model.GSLBMember) ([]*model.GSLBMember, bool) {
	if len(members) == 0 {
		return nil, false
	}

	// 1단계: healthy한 enabled 멤버만 필터링
	healthy := make([]*model.GSLBMember, 0)
	for _, member := range members {
		if !member.Enabled {
			continue
		}
		if e.healthStatus != nil {
			if v, ok := e.healthStatus.Load(member.ID); ok {
				if status, ok := v.(HealthStatus); ok && !status.Healthy {
					continue
				}
			}
		}
		healthy = append(healthy, member)
	}

	// 2단계: healthy 멤버가 있으면 가중치 기반 선택
	if len(healthy) > 0 {
		selected := e.weightedSelect(healthy)
		if selected != nil {
			return []*model.GSLBMember{selected}, false
		}
	}

	// 3단계: 모든 멤버가 unhealthy → enabled된 모든 멤버 반환
	allEnabled := make([]*model.GSLBMember, 0)
	for _, member := range members {
		if member.Enabled {
			allEnabled = append(allEnabled, member)
		}
	}

	if len(allEnabled) > 0 {
		return allEnabled, true
	}

	return nil, false
}

// weightedSelect는 가중치 기반으로 단일 멤버를 선택합니다
func (e *Engine) weightedSelect(members []*model.GSLBMember) *model.GSLBMember {
	if len(members) == 0 {
		return nil
	}

	total := int64(0)
	for _, member := range members {
		if member.Weight > 0 {
			total += member.Weight
		}
	}
	if total == 0 {
		return members[0]
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	pick := rng.Int63n(total)
	acc := int64(0)
	for _, member := range members {
		if member.Weight <= 0 {
			continue
		}
		acc += member.Weight
		if pick < acc {
			return member
		}
	}

	return members[0]
}
