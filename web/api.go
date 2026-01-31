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
	}
}
