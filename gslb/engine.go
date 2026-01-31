package gslb

import (
	"errors"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

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
		if e.matchPool(pool, clientIP) {
			matched = pool
			break
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

	selected := e.selectMember(members)
	if selected == nil {
		return nil, 0, nil
	}

	ip := net.ParseIP(selected.Address)
	if ip == nil {
		return nil, 0, errors.New("멤버 IP 파싱 실패")
	}
	return []net.IP{ip}, uint32(policy.TTL), nil
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

func (e *Engine) selectMember(members []*model.GSLBMember) *model.GSLBMember {
	if len(members) == 0 {
		return nil
	}

	enabled := make([]*model.GSLBMember, 0)
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
		enabled = append(enabled, member)
	}

	if len(enabled) == 0 {
		for _, member := range members {
			if member.Enabled {
				enabled = append(enabled, member)
			}
		}
	}

	if len(enabled) == 0 {
		return nil
	}

	total := int64(0)
	for _, member := range enabled {
		if member.Weight > 0 {
			total += member.Weight
		}
	}
	if total == 0 {
		return enabled[0]
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	pick := rng.Int63n(total)
	acc := int64(0)
	for _, member := range enabled {
		if member.Weight <= 0 {
			continue
		}
		acc += member.Weight
		if pick < acc {
			return member
		}
	}

	return enabled[0]
}
