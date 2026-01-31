package gslb

import (
	"fmt"
	"net"

	"github.com/oschwald/geoip2-golang"
)

type GeoIPResolver struct {
	reader *geoip2.Reader
}

func NewGeoIPResolver(dbPath string) (*GeoIPResolver, error) {
	reader, err := geoip2.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("GeoIP DB 로드 실패: %w", err)
	}
	return &GeoIPResolver{reader: reader}, nil
}

func (r *GeoIPResolver) Close() error {
	if r.reader == nil {
		return nil
	}
	return r.reader.Close()
}

func (r *GeoIPResolver) Country(ip string) (string, string, error) {
	if r.reader == nil {
		return "", "", fmt.Errorf("GeoIP 리더가 초기화되지 않았습니다")
	}
	record, err := r.reader.City(parseIP(ip))
	if err != nil {
		return "", "", err
	}
	return record.Country.IsoCode, record.Continent.Code, nil
}

func parseIP(ip string) net.IP {
	return net.ParseIP(ip)
}
