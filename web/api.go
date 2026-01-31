package web

import (
	"dns-go/dns"
	"dns-go/storage"
)

type API struct {
	zoneStorage     *storage.ZoneStorage
	recordStorage   *storage.RecordStorage
	upstreamStorage *storage.UpstreamStorage
	db              *storage.Database
	dnsHandler      *dns.Handler
	queryStats      *dns.QueryStats
}

func NewAPI(
	zoneStorage *storage.ZoneStorage,
	recordStorage *storage.RecordStorage,
	upstreamStorage *storage.UpstreamStorage,
	db *storage.Database,
	dnsHandler *dns.Handler,
	queryStats *dns.QueryStats,
) *API {
	return &API{
		zoneStorage:     zoneStorage,
		recordStorage:   recordStorage,
		upstreamStorage: upstreamStorage,
		db:              db,
		dnsHandler:      dnsHandler,
		queryStats:      queryStats,
	}
}
