package web

import (
	"dns-go/adblock"
	"dns-go/dns"
	"dns-go/gslb"
	"dns-go/storage"
	"sync"
)

type API struct {
	zoneStorage        *storage.ZoneStorage
	recordStorage      *storage.RecordStorage
	upstreamStorage    *storage.UpstreamStorage
	db                 *storage.Database
	dnsHandler         *dns.Handler
	queryStats         *dns.QueryStats
	policyStorage      *gslb.PolicyStorage
	poolStorage        *gslb.PoolStorage
	adblockStorage     *storage.AdblockStorage
	adblockSyncer      *adblock.Syncer
	adblockFilter      *adblock.Filter
	healthCheckStorage *gslb.HealthCheckStorage
	healthStatus       *sync.Map
	readOnly           bool // Read-Only 모드
}

func NewAPI(
	zoneStorage *storage.ZoneStorage,
	recordStorage *storage.RecordStorage,
	upstreamStorage *storage.UpstreamStorage,
	db *storage.Database,
	dnsHandler *dns.Handler,
	queryStats *dns.QueryStats,
	policyStorage *gslb.PolicyStorage,
	poolStorage *gslb.PoolStorage,
	adblockStorage *storage.AdblockStorage,
	adblockSyncer *adblock.Syncer,
	adblockFilter *adblock.Filter,
	healthCheckStorage *gslb.HealthCheckStorage,
	healthStatus *sync.Map,
) *API {
	return &API{
		zoneStorage:        zoneStorage,
		recordStorage:      recordStorage,
		upstreamStorage:    upstreamStorage,
		db:                 db,
		dnsHandler:         dnsHandler,
		queryStats:         queryStats,
		policyStorage:      policyStorage,
		poolStorage:        poolStorage,
		adblockStorage:     adblockStorage,
		adblockSyncer:      adblockSyncer,
		adblockFilter:      adblockFilter,
		healthCheckStorage: healthCheckStorage,
		healthStatus:       healthStatus,
		readOnly:           false, // 기본값: Read-Write
	}
}

// SetReadOnly는 Read-Only 모드를 설정합니다
func (api *API) SetReadOnly(readOnly bool) {
	api.readOnly = readOnly
}

// IsReadOnly는 현재 Read-Only 모드 여부를 반환합니다
func (api *API) IsReadOnly() bool {
	return api.readOnly
}
