package dns

import (
	"dns-go/gslb"
	"dns-go/model"
	"dns-go/storage"
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/miekg/dns"
)

// Handler는 DNS 쿼리를 처리하는 핸들러입니다
type Handler struct {
	cache         *DNSCache              // L1 DNS 응답 캐시
	zoneStorage   *storage.ZoneStorage   // Zone 저장소 (L2 캐시)
	recordStorage *storage.RecordStorage // Record 저장소 (L2 캐시)
	resolver      *Resolver              // 업스트림 리졸버
	cacheSettings *storage.Database      // 캐시 설정 조회용
	stats         *QueryStats
	gslbEngine    *gslb.Engine
}

// NewHandler는 새로운 DNS 핸들러를 생성합니다
func NewHandler(
	zoneStorage *storage.ZoneStorage,
	recordStorage *storage.RecordStorage,
	resolver *Resolver,
	db *storage.Database,
	stats *QueryStats,
	gslbEngine *gslb.Engine,
) (*Handler, error) {
	// DB에서 캐시 설정 로드
	var enabled, maxSize, defaultTTL, negativeTTL int64
	var prefetchTrigger float64

	query := `SELECT enabled, max_size, default_ttl, negative_ttl, prefetch_trigger FROM cache_settings WHERE id = 1`
	err := db.Reader.QueryRow(query).Scan(&enabled, &maxSize, &defaultTTL, &negativeTTL, &prefetchTrigger)
	if err != nil {
		return nil, fmt.Errorf("캐시 설정 로드 실패: %w", err)
	}

	// L1 캐시 초기화
	cache := NewDNSCache(maxSize, defaultTTL, negativeTTL, prefetchTrigger)

	handler := &Handler{
		cache:         cache,
		zoneStorage:   zoneStorage,
		recordStorage: recordStorage,
		resolver:      resolver,
		cacheSettings: db,
		stats:         stats,
		gslbEngine:    gslbEngine,
	}

	// Prefetch 콜백 함수 설정
	cache.SetPrefetchFunc(handler.handlePrefetch)

	return handler, nil
}

// ServeDNS는 DNS 쿼리를 처리합니다 (dns.Handler 인터페이스 구현)
func (h *Handler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	if h.stats != nil {
		h.stats.IncTotal()
	}
	// 응답 메시지 초기화
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Authoritative = true
	resp.RecursionAvailable = true

	// 쿼리가 없으면 에러 응답
	if len(req.Question) == 0 {
		resp.Rcode = dns.RcodeFormatError
		w.WriteMsg(resp)
		return
	}

	question := req.Question[0]
	domain := question.Name
	qtype := dns.TypeToString[question.Qtype]

	log.Printf("[DNS] Query: %s %s", domain, qtype)

	// 1. L1 캐시 확인
	if entry, ok := h.cache.Get(domain, qtype); ok {
		log.Printf("[DNS] L1 Cache HIT: %s %s", domain, qtype)
		if h.stats != nil {
			h.stats.IncL1Hit()
		}

		if entry.IsNegative {
			// NXDOMAIN 캐시
			resp.Rcode = dns.RcodeNameError
		} else {
			// 정상 응답 캐시
			resp.Answer = entry.RRs
			resp.Rcode = dns.RcodeSuccess
		}

		w.WriteMsg(resp)
		return
	}

	log.Printf("[DNS] L1 Cache MISS: %s %s", domain, qtype)
	if h.stats != nil {
		h.stats.IncL1Miss()
	}

	// 2. TODO: 광고차단 필터 체크
	// if h.adblockFilter.IsBlocked(domain) {
	//     resp.Rcode = dns.RcodeNameError
	//     h.cache.Set(domain, qtype, nil, 0, true)
	//     w.WriteMsg(resp)
	//     return
	// }

	// 3. TODO: GSLB 정책 확인
	if h.gslbEngine != nil {
		clientIP := ExtractClientIP(req)
		if clientIP == nil {
			if addr := w.RemoteAddr(); addr != nil {
				if host, _, err := net.SplitHostPort(addr.String()); err == nil {
					clientIP = net.ParseIP(host)
				}
			}
		}

		ips, ttl, err := h.gslbEngine.Resolve(domain, qtype, clientIP)
		if err != nil {
			log.Printf("[DNS] GSLB resolve error: %v", err)
		} else if len(ips) > 0 {
			answers := make([]dns.RR, 0, len(ips))
			for _, ip := range ips {
				if ip == nil {
					continue
				}
				if qtype == "A" {
					if ip4 := ip.To4(); ip4 != nil {
						answers = append(answers, &dns.A{Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl}, A: ip4})
					}
				} else if qtype == "AAAA" {
					if ip.To4() == nil {
						answers = append(answers, &dns.AAAA{Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: ttl}, AAAA: ip})
					}
				}
			}

			if len(answers) > 0 {
				resp.Answer = answers
				resp.Rcode = dns.RcodeSuccess
				cacheTTL := int64(ttl)
				if cacheTTL <= 0 {
					cacheTTL = 30
				}
				h.cache.Set(domain, qtype, resp.Answer, cacheTTL, false)
				w.WriteMsg(resp)
				return
			}
		}
	}

	// 4. Zone 조회 (L2 캐시 활용)
	zoneName := h.extractDomain(domain)
	zone, err := h.zoneStorage.GetZoneByName(zoneName)
	if err != nil {
		log.Printf("[DNS] Zone 조회 에러: %v", err)
		resp.Rcode = dns.RcodeServerFailure
		w.WriteMsg(resp)
		return
	}

	// Zone이 존재하면 Record 조회
	if zone != nil {
		log.Printf("[DNS] Zone found: %s (ID: %d)", zone.Name, zone.ID)

		// Record 조회 (L2 캐시 활용)
		records, err := h.recordStorage.GetRecordsByName(domain, qtype)
		if err != nil {
			log.Printf("[DNS] Record 조회 에러: %v", err)
			resp.Rcode = dns.RcodeServerFailure
			w.WriteMsg(resp)
			return
		}

		// 레코드가 있으면 응답 생성
		if len(records) > 0 {
			log.Printf("[DNS] Records found: %d records", len(records))

			// 응답 생성
			answer := h.buildResponse(question, records)
			resp.Answer = answer.Answer
			resp.Rcode = dns.RcodeSuccess

			// L1 캐시에 저장 (최소 TTL 사용)
			minTTL := int64(300) // 기본값
			if len(records) > 0 {
				minTTL = records[0].TTL
				for _, r := range records {
					if r.TTL < minTTL {
						minTTL = r.TTL
					}
				}
			}
			h.cache.Set(domain, qtype, resp.Answer, minTTL, false)

			w.WriteMsg(resp)
			return
		}

		// 레코드가 없으면 NXDOMAIN (Zone은 존재하지만 레코드가 없음)
		log.Printf("[DNS] No records found for %s %s in zone %s", domain, qtype, zone.Name)
	}

	// 5. Zone 또는 Record가 없으면 업스트림 포워딩
	log.Printf("[DNS] Forwarding to upstream: %s %s", domain, qtype)
	upstreamResp, err := h.resolver.Forward(req)
	if err != nil {
		log.Printf("[DNS] Upstream forwarding failed: %v", err)

		// NXDOMAIN 캐시
		resp.Rcode = dns.RcodeNameError
		h.cache.Set(domain, qtype, nil, 0, true)
		w.WriteMsg(resp)
		return
	}
	if h.stats != nil {
		h.stats.IncUpstreamHit()
	}

	// 업스트림 응답 캐싱 (TTL 준수)
	if len(upstreamResp.Answer) > 0 {
		// 최소 TTL 추출
		minTTL := int64(upstreamResp.Answer[0].Header().Ttl)
		for _, rr := range upstreamResp.Answer {
			ttl := int64(rr.Header().Ttl)
			if ttl < minTTL {
				minTTL = ttl
			}
		}

		h.cache.Set(domain, qtype, upstreamResp.Answer, minTTL, false)
		log.Printf("[DNS] Cached upstream response: %s %s (TTL: %d)", domain, qtype, minTTL)
	} else if upstreamResp.Rcode == dns.RcodeNameError {
		// NXDOMAIN 캐싱
		h.cache.Set(domain, qtype, nil, 0, true)
	}

	// 6. 응답 반환
	w.WriteMsg(upstreamResp)
}

// buildResponse는 레코드를 DNS 응답으로 변환합니다
func (h *Handler) buildResponse(question dns.Question, records []*model.Record) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetQuestion(question.Name, question.Qtype)

	for _, record := range records {
		if !record.Enabled {
			continue
		}

		rr := h.recordToRR(record)
		if rr != nil {
			resp.Answer = append(resp.Answer, rr)
		}
	}

	return resp
}

// recordToRR은 model.Record를 dns.RR로 변환합니다
func (h *Handler) recordToRR(record *model.Record) dns.RR {
	header := dns.RR_Header{
		Name:   record.Name,
		Rrtype: dns.StringToType[record.Type],
		Class:  dns.ClassINET,
		Ttl:    uint32(record.TTL),
	}

	switch record.Type {
	case "A":
		return &dns.A{
			Hdr: header,
			A:   net.ParseIP(record.Content),
		}

	case "AAAA":
		return &dns.AAAA{
			Hdr:  header,
			AAAA: net.ParseIP(record.Content),
		}

	case "CNAME":
		return &dns.CNAME{
			Hdr:    header,
			Target: record.Content,
		}

	case "MX":
		return &dns.MX{
			Hdr:        header,
			Preference: uint16(record.Priority),
			Mx:         record.Content,
		}

	case "TXT":
		return &dns.TXT{
			Hdr: header,
			Txt: []string{record.Content},
		}

	case "NS":
		return &dns.NS{
			Hdr: header,
			Ns:  record.Content,
		}

	case "SOA":
		// SOA 레코드는 Zone 정보에서 생성되어야 하지만
		// Record 테이블에도 저장될 수 있음
		// Content 포맷: "mname rname serial refresh retry expire minimum"
		parts := strings.Fields(record.Content)
		if len(parts) >= 7 {
			return &dns.SOA{
				Hdr:     header,
				Ns:      parts[0],
				Mbox:    parts[1],
				Serial:  parseUint32(parts[2]),
				Refresh: parseUint32(parts[3]),
				Retry:   parseUint32(parts[4]),
				Expire:  parseUint32(parts[5]),
				Minttl:  parseUint32(parts[6]),
			}
		}

	default:
		log.Printf("[DNS] Unsupported record type: %s", record.Type)
		return nil
	}

	return nil
}

// extractDomain은 FQDN에서 Zone 이름을 추출합니다
// 예: "www.example.com." -> "example.com."
//
//	"api.sub.example.com." -> "example.com." (단, sub.example.com. Zone이 없을 때)
func (h *Handler) extractDomain(fqdn string) string {
	// FQDN을 점으로 분할
	parts := strings.Split(strings.TrimSuffix(fqdn, "."), ".")
	if len(parts) == 0 {
		return fqdn
	}

	// 가장 긴 도메인부터 확인 (예: sub.example.com. -> example.com. -> com.)
	for i := 0; i < len(parts); i++ {
		candidate := strings.Join(parts[i:], ".") + "."

		// Zone 존재 여부 확인
		zone, err := h.zoneStorage.GetZoneByName(candidate)
		if err == nil && zone != nil {
			return candidate
		}
	}

	// Zone을 찾지 못하면 루트 도메인 반환 (예: example.com.)
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], ".") + "."
	}

	return fqdn
}

// handlePrefetch는 Prefetch 콜백 함수입니다 (백그라운드 갱신)
func (h *Handler) handlePrefetch(domain, qtype string) {
	log.Printf("[DNS] Prefetch triggered: %s %s", domain, qtype)

	// 백그라운드에서 레코드 갱신
	// 1. Zone 조회
	zoneName := h.extractDomain(domain)
	zone, err := h.zoneStorage.GetZoneByName(zoneName)
	if err != nil || zone == nil {
		log.Printf("[DNS] Prefetch failed - Zone not found: %s", zoneName)
		return
	}

	// 2. Record 조회
	records, err := h.recordStorage.GetRecordsByName(domain, qtype)
	if err != nil {
		log.Printf("[DNS] Prefetch failed - Record query error: %v", err)
		return
	}

	// 3. 레코드가 있으면 캐시 갱신
	if len(records) > 0 {
		question := dns.Question{
			Name:   domain,
			Qtype:  dns.StringToType[qtype],
			Qclass: dns.ClassINET,
		}

		answer := h.buildResponse(question, records)

		// 최소 TTL 계산
		minTTL := records[0].TTL
		for _, r := range records {
			if r.TTL < minTTL {
				minTTL = r.TTL
			}
		}

		// 캐시 갱신
		h.cache.Set(domain, qtype, answer.Answer, minTTL, false)
		log.Printf("[DNS] Prefetch completed: %s %s (%d records)", domain, qtype, len(records))
	} else {
		// 레코드가 없으면 업스트림 조회
		req := new(dns.Msg)
		req.SetQuestion(domain, dns.StringToType[qtype])

		upstreamResp, err := h.resolver.Forward(req)
		if err != nil {
			log.Printf("[DNS] Prefetch upstream failed: %v", err)
			return
		}

		if len(upstreamResp.Answer) > 0 {
			// 최소 TTL 추출
			minTTL := int64(upstreamResp.Answer[0].Header().Ttl)
			for _, rr := range upstreamResp.Answer {
				ttl := int64(rr.Header().Ttl)
				if ttl < minTTL {
					minTTL = ttl
				}
			}

			h.cache.Set(domain, qtype, upstreamResp.Answer, minTTL, false)
			log.Printf("[DNS] Prefetch from upstream completed: %s %s", domain, qtype)
		}
	}
}

// parseUint32는 문자열을 uint32로 파싱합니다
func parseUint32(s string) uint32 {
	var result uint32
	fmt.Sscanf(s, "%d", &result)
	return result
}

// GetCache는 L1 캐시를 반환합니다 (테스트용)
func (h *Handler) GetCache() *DNSCache {
	return h.cache
}

// ReconfigureCache applies new cache settings to L1 cache
func (h *Handler) ReconfigureCache(settings *model.CacheSettings) {
	if settings == nil {
		return
	}
	cache := NewDNSCache(settings.MaxSize, settings.DefaultTTL, settings.NegativeTTL, settings.PrefetchTrigger)
	cache.SetPrefetchFunc(h.handlePrefetch)
	h.cache = cache
}
