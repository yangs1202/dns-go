package dns

import (
	"dns-go/adblock"
	"dns-go/gslb"
	"dns-go/metrics"
	"dns-go/model"
	"dns-go/storage"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Handler는 DNS 쿼리를 처리하는 핸들러입니다
type Handler struct {
	cacheMu          sync.RWMutex
	cache            *DNSCache              // L1 DNS 응답 캐시
	zoneStorage      *storage.ZoneStorage   // Zone 저장소 (L2 캐시)
	recordStorage    *storage.RecordStorage // Record 저장소 (L2 캐시)
	lastQueryTracker *lastQueryTracker
	queryLogWriter   *QueryLogWriter   // DNS 쿼리 로그 라이터
	resolver         *Resolver         // 업스트림 리졸버
	cacheSettings    *storage.Database // 캐시 설정 조회용
	stats            *QueryStats
	gslbEngine       *gslb.Engine
	adblockFilter    *adblock.Filter
	adblockStorage   *storage.AdblockStorage
	adblockResponse  string
	nsid             string // RFC 5001 NSID (Name Server Identifier)
	version          string // CHAOS TXT version.bind 응답
	negativeTTL      uint32 // NXDOMAIN 응답 TTL (SOA Minimum)
}

const maxCNAMEChainDepth = 8

var dnsDebugLogsEnabled = strings.EqualFold(os.Getenv("DNS_GO_DEBUG_LOGS"), "true") ||
	os.Getenv("DNS_GO_DEBUG_LOGS") == "1"

func debugLogf(format string, args ...interface{}) {
	if dnsDebugLogsEnabled {
		log.Printf(format, args...)
	}
}

// NewHandler는 새로운 DNS 핸들러를 생성합니다
func NewHandler(
	zoneStorage *storage.ZoneStorage,
	recordStorage *storage.RecordStorage,
	resolver *Resolver,
	db *storage.Database,
	stats *QueryStats,
	gslbEngine *gslb.Engine,
	adblockFilter *adblock.Filter,
	adblockStorage *storage.AdblockStorage,
	adblockResponse string,
	nsid string,
	version string,
	queryLogWriter *QueryLogWriter,
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
		cache:            cache,
		zoneStorage:      zoneStorage,
		recordStorage:    recordStorage,
		lastQueryTracker: newLastQueryTracker(recordStorage, defaultLastQueryFlushInterval),
		queryLogWriter:   queryLogWriter,
		resolver:         resolver,
		cacheSettings:    db,
		stats:            stats,
		gslbEngine:       gslbEngine,
		adblockFilter:    adblockFilter,
		adblockStorage:   adblockStorage,
		adblockResponse:  adblockResponse,
		nsid:             nsid,
		version:          version,
		negativeTTL:      uint32(negativeTTL),
	}

	// Prefetch 콜백 함수 설정
	cache.SetPrefetchFunc(handler.handlePrefetch)

	return handler, nil
}

// Stop는 핸들러 내부 백그라운드 작업을 종료합니다.
func (h *Handler) Stop() {
	if cache := h.GetCache(); cache != nil {
		cache.Stop()
	}
	if h.lastQueryTracker != nil {
		h.lastQueryTracker.Stop()
	}
	if h.queryLogWriter != nil {
		h.queryLogWriter.Stop()
	}
}

// logQuery는 DNS 요청/응답 로그를 기록합니다
func (h *Handler) logQuery(w dns.ResponseWriter, req *dns.Msg, resp *dns.Msg, source string, start time.Time) {
	if h.queryLogWriter == nil || len(req.Question) == 0 {
		return
	}

	clientIP := ""
	protocol := "udp"
	if addr := w.RemoteAddr(); addr != nil {
		if host, _, err := net.SplitHostPort(addr.String()); err == nil {
			clientIP = host
		}
		if _, ok := addr.(*net.TCPAddr); ok {
			protocol = "tcp"
		}
	}

	ednsPresent := false
	ednsVersion := 0
	ednsBufferSize := 0
	if opt := req.IsEdns0(); opt != nil {
		ednsPresent = true
		ednsVersion = int(opt.Version())
		ednsBufferSize = int(opt.UDPSize())
	}

	entry := &model.QueryLog{
		Timestamp:      start.UTC(),
		ClientIP:       clientIP,
		Domain:         strings.ToLower(req.Question[0].Name),
		QueryType:      dns.TypeToString[req.Question[0].Qtype],
		ResponseCode:   dns.RcodeToString[resp.Rcode],
		ResponseSource: source,
		LatencyMs:      float64(time.Since(start).Microseconds()) / 1000.0,
		ResponseData:   summarizeAnswers(resp.Answer),
		Protocol:       protocol,
		ResponseSize:   resp.Len(),
		EDNSPresent:    ednsPresent,
		EDNSVersion:    ednsVersion,
		EDNSBufferSize: ednsBufferSize,
	}

	h.queryLogWriter.Record(entry)
}

// summarizeAnswers는 응답 레코드를 요약 문자열로 변환합니다
func summarizeAnswers(rrs []dns.RR) string {
	if len(rrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(rrs))
	for _, rr := range rrs {
		switch v := rr.(type) {
		case *dns.A:
			parts = append(parts, v.A.String())
		case *dns.AAAA:
			parts = append(parts, v.AAAA.String())
		case *dns.CNAME:
			parts = append(parts, "CNAME:"+v.Target)
		case *dns.MX:
			parts = append(parts, fmt.Sprintf("MX:%d:%s", v.Preference, v.Mx))
		case *dns.TXT:
			parts = append(parts, "TXT:"+strings.Join(v.Txt, " "))
		case *dns.NS:
			parts = append(parts, "NS:"+v.Ns)
		case *dns.SOA:
			parts = append(parts, "SOA:"+v.Ns)
		case *dns.SRV:
			parts = append(parts, fmt.Sprintf("SRV:%d:%d:%d:%s", v.Priority, v.Weight, v.Port, v.Target))
		case *dns.PTR:
			parts = append(parts, "PTR:"+v.Ptr)
		default:
			parts = append(parts, rr.String())
		}
	}
	return strings.Join(parts, ", ")
}

// ClearCache는 모든 DNS 캐시를 클리어합니다 (동기화 시 호출)
func (h *Handler) ClearCache() {
	if cache := h.GetCache(); cache != nil {
		cache.Clear()
		log.Println("DNS L1 캐시 클리어 완료")
	}

	// L2 캐시(Zone/Record Storage)도 클리어
	if h.zoneStorage != nil {
		h.zoneStorage.ClearCache()
		log.Println("DNS L2 Zone 캐시 클리어 완료")
	}
	if h.recordStorage != nil {
		h.recordStorage.ClearCache()
		log.Println("DNS L2 Record 캐시 클리어 완료")
	}
}

// buildSOA는 NXDOMAIN 응답용 기본 SOA 레코드를 생성합니다
func (h *Handler) buildSOA(zoneName string) *dns.SOA {
	if !strings.HasSuffix(zoneName, ".") {
		zoneName = zoneName + "."
	}

	return &dns.SOA{
		Hdr:     dns.RR_Header{Name: zoneName, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: h.negativeTTL},
		Ns:      "ns." + zoneName,
		Mbox:    "admin." + zoneName,
		Serial:  1,
		Refresh: 3600,
		Retry:   900,
		Expire:  86400,
		Minttl:  h.negativeTTL, // NXDOMAIN 캐싱 시간
	}
}

// handleCHAOS는 CHAOS 클래스 쿼리를 처리합니다
func (h *Handler) handleCHAOS(w dns.ResponseWriter, req *dns.Msg, resp *dns.Msg, start time.Time) {
	question := req.Question[0]
	domain := strings.ToLower(question.Name)

	// version.bind TXT 쿼리
	if (domain == "version.bind." || domain == "version.server.") && question.Qtype == dns.TypeTXT {
		log.Printf("[DNS] CHAOS version.bind query")
		resp.Authoritative = true
		resp.Answer = []dns.RR{
			&dns.TXT{
				Hdr: dns.RR_Header{
					Name:   question.Name,
					Rrtype: dns.TypeTXT,
					Class:  dns.ClassCHAOS,
					Ttl:    0, // CHAOS 응답은 TTL=0
				},
				Txt: []string{h.version},
			},
		}
		resp.Rcode = dns.RcodeSuccess
		h.logQuery(w, req, resp, "chaos", start)
		_ = w.WriteMsg(resp)
		return
	}

	// hostname.bind TXT 쿼리
	if (domain == "hostname.bind." || domain == "hostname.server.") && question.Qtype == dns.TypeTXT {
		log.Printf("[DNS] CHAOS hostname.bind query")
		resp.Authoritative = true
		resp.Answer = []dns.RR{
			&dns.TXT{
				Hdr: dns.RR_Header{
					Name:   question.Name,
					Rrtype: dns.TypeTXT,
					Class:  dns.ClassCHAOS,
					Ttl:    0,
				},
				Txt: []string{h.nsid},
			},
		}
		resp.Rcode = dns.RcodeSuccess
		h.logQuery(w, req, resp, "chaos", start)
		_ = w.WriteMsg(resp)
		return
	}

	// id.server TXT 쿼리
	if domain == "id.server." && question.Qtype == dns.TypeTXT {
		log.Printf("[DNS] CHAOS id.server query")
		resp.Authoritative = true
		resp.Answer = []dns.RR{
			&dns.TXT{
				Hdr: dns.RR_Header{
					Name:   question.Name,
					Rrtype: dns.TypeTXT,
					Class:  dns.ClassCHAOS,
					Ttl:    0,
				},
				Txt: []string{h.nsid},
			},
		}
		resp.Rcode = dns.RcodeSuccess
		h.logQuery(w, req, resp, "chaos", start)
		_ = w.WriteMsg(resp)
		return
	}

	// 지원하지 않는 CHAOS 쿼리 - REFUSED
	log.Printf("[DNS] Unsupported CHAOS query: %s", domain)
	resp.Rcode = dns.RcodeRefused
	h.logQuery(w, req, resp, "chaos", start)
	_ = w.WriteMsg(resp)
}

// setEDNS0는 EDNS0 OPT 레코드를 응답에 추가합니다
func (h *Handler) setEDNS0(resp *dns.Msg, req *dns.Msg) {
	// 요청에 EDNS0가 있는지 확인
	if opt := req.IsEdns0(); opt != nil {
		// Cloudflare 방식: 클라이언트 요청과 무관하게 1232로 고정
		// 이유: IPv6 MTU(1280) - IP/UDP 헤더(48) = 1232
		// RFC 8899 권장, IP fragmentation 완전 방지
		resp.SetEdns0(1232, false)

		// RFC 5001: NSID (Name Server Identifier) 지원
		// 클라이언트가 NSID 요청하면 응답에 포함
		nsidRequested := false
		for _, option := range opt.Option {
			if _, ok := option.(*dns.EDNS0_NSID); ok {
				nsidRequested = true
				break
			}
		}

		if nsidRequested && h.nsid != "" {
			if respOpt := resp.IsEdns0(); respOpt != nil {
				nsidOpt := &dns.EDNS0_NSID{
					Code: dns.EDNS0NSID,
					Nsid: hex.EncodeToString([]byte(h.nsid)),
				}
				respOpt.Option = append(respOpt.Option, nsidOpt)
			}
		}
	}
}

// ServeDNS는 DNS 쿼리를 처리합니다 (dns.Handler 인터페이스 구현)
func (h *Handler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	start := time.Now()
	if h.stats != nil {
		h.stats.IncTotal()
	}
	// 응답 메시지 초기화
	resp := new(dns.Msg)
	resp.SetReply(req)

	// RFC 1035 준수:
	// - Authoritative: 권한 서버인 경우만 설정 (나중에 Zone 응답 시 설정)
	// - RecursionAvailable: 재귀 가능 여부 (항상 true)
	resp.Authoritative = false // 기본값: 재귀 응답은 non-authoritative
	resp.RecursionAvailable = true

	// EDNS0 지원 추가 (클라이언트가 요청한 경우)
	h.setEDNS0(resp, req)

	// 쿼리가 없으면 에러 응답
	if len(req.Question) == 0 {
		resp.Rcode = dns.RcodeFormatError
		metrics.QueriesTotal.WithLabelValues("", dns.RcodeToString[dns.RcodeFormatError]).Inc()
		h.logQuery(w, req, resp, "error", start)
		_ = w.WriteMsg(resp)
		return
	}

	question := req.Question[0]
	domain := strings.ToLower(question.Name)
	qtype := dns.TypeToString[question.Qtype]

	debugLogf("[DNS] Query: %s %s (class: %s)", domain, qtype, dns.ClassToString[question.Qclass])

	// CHAOS 클래스 처리 (version.bind, hostname.bind 등)
	if question.Qclass == dns.ClassCHAOS {
		h.handleCHAOS(w, req, resp, start)
		return
	}

	// ANY 쿼리 차단 (RFC 8482 - DDoS 증폭 공격 방지)
	if question.Qtype == dns.TypeANY {
		debugLogf("[DNS] ANY query blocked: %s (RFC 8482)", domain)
		resp.Rcode = dns.RcodeNotImplemented
		metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeNotImplemented]).Inc()
		h.logQuery(w, req, resp, "blocked", start)
		_ = w.WriteMsg(resp)
		return
	}

	// 조회 시각은 domain 기준으로 최신값만 비동기 반영합니다.
	if h.lastQueryTracker != nil {
		h.lastQueryTracker.Record(domain, start)
	}

	// 1. L1 캐시 확인
	cache := h.GetCache()
	if entry, ok := cache.Get(domain, qtype); ok {
		debugLogf("[DNS] L1 Cache HIT: %s %s", domain, qtype)
		if h.stats != nil {
			h.stats.IncL1Hit()
		}

		if entry.IsNegative {
			// NXDOMAIN 캐시
			resp.Rcode = dns.RcodeNameError
			zoneName := h.extractDomain(domain)
			resp.Ns = []dns.RR{h.buildSOA(zoneName)}
		} else {
			// 정상 응답 캐시
			resp.Answer = entry.RRs
			resp.Rcode = dns.RcodeSuccess
		}

		metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[resp.Rcode]).Inc()
		metrics.QueryDurationSeconds.WithLabelValues("cache").Observe(time.Since(start).Seconds())
		h.logQuery(w, req, resp, "cache", start)
		_ = w.WriteMsg(resp)
		return
	}

	debugLogf("[DNS] L1 Cache MISS: %s %s", domain, qtype)
	if h.stats != nil {
		h.stats.IncL1Miss()
	}

	// 2. 광고차단 필터 체크
	if h.adblockFilter != nil {
		blocked, err := h.adblockFilter.IsBlocked(domain)
		if err != nil {
			log.Printf("[DNS] Adblock check error: %v", err)
		} else if blocked {
			metrics.QueriesBlockedTotal.Inc()
			metrics.AdblockBlockedTotal.Inc()
			clientIP := ExtractClientIP(req)
			if clientIP == nil {
				if addr := w.RemoteAddr(); addr != nil {
					if host, _, err := net.SplitHostPort(addr.String()); err == nil {
						clientIP = net.ParseIP(host)
					}
				}
			}
			if h.adblockStorage != nil && clientIP != nil {
				_ = h.adblockStorage.RecordBlockedQuery(domain, clientIP.String())
			}
			if strings.ToUpper(h.adblockResponse) == "NXDOMAIN" {
				resp.Rcode = dns.RcodeNameError
				zoneName := h.extractDomain(domain)
				resp.Ns = []dns.RR{h.buildSOA(zoneName)}
				cache.Set(domain, qtype, nil, 0, true)
				metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeNameError]).Inc()
				metrics.QueryDurationSeconds.WithLabelValues("adblock").Observe(time.Since(start).Seconds())
				h.logQuery(w, req, resp, "adblock", start)
				_ = w.WriteMsg(resp)
				return
			}
			if qtype == "A" {
				resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("0.0.0.0")}}
				resp.Rcode = dns.RcodeSuccess
				cache.Set(domain, qtype, resp.Answer, 60, false)
				metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeSuccess]).Inc()
				metrics.QueryDurationSeconds.WithLabelValues("adblock").Observe(time.Since(start).Seconds())
				h.logQuery(w, req, resp, "adblock", start)
				_ = w.WriteMsg(resp)
				return
			}
			if qtype == "AAAA" {
				resp.Answer = []dns.RR{&dns.AAAA{Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60}, AAAA: net.ParseIP("::")}}
				resp.Rcode = dns.RcodeSuccess
				cache.Set(domain, qtype, resp.Answer, 60, false)
				metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeSuccess]).Inc()
				metrics.QueryDurationSeconds.WithLabelValues("adblock").Observe(time.Since(start).Seconds())
				h.logQuery(w, req, resp, "adblock", start)
				_ = w.WriteMsg(resp)
				return
			}
			resp.Rcode = dns.RcodeNameError
			zoneName := h.extractDomain(domain)
			resp.Ns = []dns.RR{h.buildSOA(zoneName)}
			cache.Set(domain, qtype, nil, 0, true)
			metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeNameError]).Inc()
			metrics.QueryDurationSeconds.WithLabelValues("adblock").Observe(time.Since(start).Seconds())
			h.logQuery(w, req, resp, "adblock", start)
			_ = w.WriteMsg(resp)
			return
		}
	}

	// 3. Client IP 추출 (GSLB 사용 시)
	var clientIP net.IP
	if h.gslbEngine != nil {
		clientIP = ExtractClientIP(req)
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
				switch qtype {
				case "A":
					if ip4 := ip.To4(); ip4 != nil {
						answers = append(answers, &dns.A{Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl}, A: ip4})
					}
				case "AAAA":
					if ip.To4() == nil {
						answers = append(answers, &dns.AAAA{Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: ttl}, AAAA: ip})
					}
				}
			}

			if len(answers) > 0 {
				resp.Answer = answers
				resp.Rcode = dns.RcodeSuccess
				// GSLB 응답은 캐시하지 않음 (클라이언트 IP 기반 동적 응답)
				metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeSuccess]).Inc()
				metrics.QueryDurationSeconds.WithLabelValues("gslb").Observe(time.Since(start).Seconds())
				h.logQuery(w, req, resp, "gslb", start)
				_ = w.WriteMsg(resp)
				return
			}
		}

		// GSLB 도메인이지만 해당 qtype에 맞는 레코드가 없는 경우 (예: A만 있고 AAAA 쿼리)
		// RFC 4074: 도메인이 존재하면 NOERROR (빈 응답) 반환, NXDOMAIN 아님
		if h.gslbEngine.HasDomain(domain) {
			debugLogf("[DNS] GSLB domain %s exists but no %s record, returning NOERROR (RFC 4074)", domain, qtype)
			resp.Rcode = dns.RcodeSuccess
			zoneName := h.extractDomain(domain)
			resp.Ns = []dns.RR{h.buildSOA(zoneName)}
			cache.Set(domain, qtype, nil, 0, false) // 빈 응답도 캐시 (AAAA 반복 쿼리 방지)
			metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeSuccess]).Inc()
			metrics.QueryDurationSeconds.WithLabelValues("gslb").Observe(time.Since(start).Seconds())
			h.logQuery(w, req, resp, "gslb", start)
			_ = w.WriteMsg(resp)
			return
		}
	}

	// 4. Zone 조회 (L2 캐시 활용)
	zoneName := h.extractDomain(domain)
	zone, err := h.zoneStorage.GetZoneByName(zoneName)
	if err != nil {
		log.Printf("[DNS] Zone 조회 에러: %v", err)
		resp.Rcode = dns.RcodeServerFailure
		metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeServerFailure]).Inc()
		h.logQuery(w, req, resp, "error", start)
		_ = w.WriteMsg(resp)
		return
	}

	// Zone이 존재하면 Record 조회
	if zone != nil {
		debugLogf("[DNS] Zone found: %s (ID: %d)", zone.Name, zone.ID)

		// Record 조회 (L2 캐시 활용)
		records, err := h.recordStorage.GetRecordsByNameAndZone(zone.ID, domain, qtype)
		if err != nil {
			log.Printf("[DNS] Record 조회 에러: %v", err)
			resp.Rcode = dns.RcodeServerFailure
			metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeServerFailure]).Inc()
			h.logQuery(w, req, resp, "error", start)
			_ = w.WriteMsg(resp)
			return
		}

		// A/AAAA 레코드가 없으면 CNAME 체인 확인
		cnameResolvedViaGSLB := false
		if len(records) == 0 && (qtype == "A" || qtype == "AAAA") {
			chainRecords, viaGSLB, err := h.resolveCNAMEChain(zone.ID, domain, qtype, clientIP)
			if err != nil {
				log.Printf("[DNS] CNAME 체인 조회 에러: %v", err)
			}
			if len(chainRecords) > 0 {
				records = chainRecords
				cnameResolvedViaGSLB = viaGSLB
			}
		}

		// 레코드가 있으면 응답 생성
		if len(records) > 0 {
			debugLogf("[DNS] Records found: %d records", len(records))

			// RFC 1035: Zone에서 직접 응답하는 경우 Authoritative
			resp.Authoritative = true

			// 응답 생성
			answer := h.buildResponse(question, records)
			resp.Answer = answer.Answer
			resp.Rcode = dns.RcodeSuccess

			// RFC 1035: Authoritative 응답에는 AUTHORITY 섹션에 NS 레코드 추가
			nsRecords, err := h.recordStorage.GetRecordsByNameAndZone(zone.ID, zoneName, "NS")
			if err == nil && len(nsRecords) > 0 {
				for _, nsRecord := range nsRecords {
					if nsRecord.Enabled {
						if nsRR := h.recordToRR(nsRecord); nsRR != nil {
							resp.Ns = append(resp.Ns, nsRR)
						}
					}
				}
			}

			// L1 캐시에 저장 (GSLB 경유 응답은 캐시하지 않음)
			if cnameResolvedViaGSLB {
				debugLogf("[DNS] Skipping L1 cache for %s %s (CNAME target resolved via GSLB)", domain, qtype)
			} else {
				minTTL := int64(300) // 기본값
				if len(records) > 0 {
					minTTL = records[0].TTL
					for _, r := range records {
						if r.TTL < minTTL {
							minTTL = r.TTL
						}
					}
				}
				cache.Set(domain, qtype, resp.Answer, minTTL, false)
			}

			metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeSuccess]).Inc()
			metrics.QueryDurationSeconds.WithLabelValues("zone").Observe(time.Since(start).Seconds())
			h.logQuery(w, req, resp, "zone", start)
			_ = w.WriteMsg(resp)
			return
		}

		// Zone은 있지만 Record가 없는 경우
		if !zone.AllowFallback {
			// Fallback 비활성화 (Authoritative) → 도메인 존재 여부 확인
			// RFC 1035: 도메인이 존재하면 NOERROR, 존재하지 않으면 NXDOMAIN
			domainExists, err := h.recordStorage.DomainExistsInZone(zone.ID, domain)
			if err != nil {
				log.Printf("[DNS] Failed to check domain existence: %v", err)
			}

			if !domainExists {
				// 도메인 자체가 존재하지 않음 → NXDOMAIN
				debugLogf("[DNS] Domain %s does not exist in zone %s (authoritative), returning NXDOMAIN", domain, zone.Name)
				resp.Rcode = dns.RcodeNameError
				resp.Ns = []dns.RR{h.buildSOA(zoneName)}
				metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeNameError]).Inc()
				metrics.QueryDurationSeconds.WithLabelValues("zone").Observe(time.Since(start).Seconds())
				h.logQuery(w, req, resp, "zone", start)
				_ = w.WriteMsg(resp)
				return
			}

			// 도메인은 존재하지만 해당 타입의 레코드가 없음 → NOERROR (빈 응답)
			debugLogf("[DNS] Domain %s exists but no %s record, returning NOERROR", domain, qtype)
			resp.Rcode = dns.RcodeSuccess
			resp.Ns = []dns.RR{h.buildSOA(zoneName)}
			cache.Set(domain, qtype, nil, 0, false) // 빈 응답도 캐시 (AAAA 반복 쿼리 방지)
			metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeSuccess]).Inc()
			metrics.QueryDurationSeconds.WithLabelValues("zone").Observe(time.Since(start).Seconds())
			h.logQuery(w, req, resp, "zone", start)
			_ = w.WriteMsg(resp)
			return
		}

		// Fallback 허용 → 도메인 존재 여부 먼저 확인
		// RFC 4074: 도메인이 존재하면 (다른 타입 레코드가 있으면) NOERROR 반환
		// 예: A 레코드만 있는 도메인에 AAAA 쿼리 → NOERROR (빈 응답)
		domainExists, err := h.recordStorage.DomainExistsInZone(zone.ID, domain)
		if err != nil {
			log.Printf("[DNS] Failed to check domain existence: %v", err)
		}
		if domainExists {
			debugLogf("[DNS] Domain %s exists in zone %s but no %s record, returning NOERROR (RFC 4074)", domain, zone.Name, qtype)
			resp.Authoritative = true
			resp.Rcode = dns.RcodeSuccess
			resp.Ns = []dns.RR{h.buildSOA(zoneName)}
			cache.Set(domain, qtype, nil, 0, false) // 빈 응답도 캐시 (AAAA 반복 쿼리 방지)
			metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeSuccess]).Inc()
			metrics.QueryDurationSeconds.WithLabelValues("zone").Observe(time.Since(start).Seconds())
			h.logQuery(w, req, resp, "zone", start)
			_ = w.WriteMsg(resp)
			return
		}

		// 도메인 자체가 없으면 Upstream으로 포워딩
		debugLogf("[DNS] Zone %s exists but domain %s not found, falling back to upstream", zone.Name, domain)
	}

	// 5. Zone 또는 Record가 없으면 업스트림 포워딩
	// RFC 1035: RD=0(+norecurse)이면 재귀 처리 안 함
	if !req.RecursionDesired {
		debugLogf("[DNS] Recursion not desired (RD=0), returning REFUSED")
		resp.Rcode = dns.RcodeRefused
		metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeRefused]).Inc()
		h.logQuery(w, req, resp, "refused", start)
		_ = w.WriteMsg(resp)
		return
	}

	debugLogf("[DNS] Forwarding to upstream: %s %s", domain, qtype)
	upstreamResp, err := h.resolver.Forward(req)
	if err != nil {
		log.Printf("[DNS] Upstream forwarding failed: %v", err)

		// NXDOMAIN 캐시
		resp.Rcode = dns.RcodeNameError
		zoneName := h.extractDomain(domain)
		resp.Ns = []dns.RR{h.buildSOA(zoneName)}
		cache.Set(domain, qtype, nil, 0, true)
		metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[dns.RcodeNameError]).Inc()
		metrics.QueryDurationSeconds.WithLabelValues("upstream").Observe(time.Since(start).Seconds())
		h.logQuery(w, req, resp, "upstream", start)
		_ = w.WriteMsg(resp)
		return
	}
	if h.stats != nil {
		h.stats.IncUpstreamHit()
	}
	metrics.QueriesForwardedTotal.Inc()

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

		cache.Set(domain, qtype, upstreamResp.Answer, minTTL, false)
		debugLogf("[DNS] Cached upstream response: %s %s (TTL: %d)", domain, qtype, minTTL)
	} else if upstreamResp.Rcode == dns.RcodeNameError {
		// NXDOMAIN 캐싱
		cache.Set(domain, qtype, nil, 0, true)
	}

	// 6. 응답 반환
	metrics.QueriesTotal.WithLabelValues(qtype, dns.RcodeToString[upstreamResp.Rcode]).Inc()
	metrics.QueryDurationSeconds.WithLabelValues("upstream").Observe(time.Since(start).Seconds())
	h.logQuery(w, req, upstreamResp, "upstream", start)
	_ = w.WriteMsg(upstreamResp)
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

// resolveCNAMEChain은 A/AAAA 질의 시 CNAME 체인을 최대 maxCNAMEChainDepth 단계까지 추적합니다.
// 반환값: records, resolvedViaGSLB, error
func (h *Handler) resolveCNAMEChain(zoneID int64, domain, qtype string, clientIP net.IP) ([]*model.Record, bool, error) {
	records := make([]*model.Record, 0, maxCNAMEChainDepth+1)
	visited := make(map[string]struct{}, maxCNAMEChainDepth+1)
	current := domain
	resolvedViaGSLB := false

	for depth := 0; depth < maxCNAMEChainDepth; depth++ {
		if _, exists := visited[current]; exists {
			log.Printf("[DNS] CNAME loop detected at %s", current)
			break
		}
		visited[current] = struct{}{}

		cnameRecords, err := h.recordStorage.GetRecordsByNameAndZone(zoneID, current, "CNAME")
		if err != nil {
			return records, false, err
		}
		if len(cnameRecords) == 0 {
			// 크로스 Zone CNAME 탐색
			currentZoneName := h.extractDomain(current)
			currentZone, zErr := h.zoneStorage.GetZoneByName(currentZoneName)
			if zErr == nil && currentZone != nil && currentZone.ID != zoneID {
				cnameRecords, err = h.recordStorage.GetRecordsByNameAndZone(currentZone.ID, current, "CNAME")
				if err != nil {
					return records, false, err
				}
				if len(cnameRecords) > 0 {
					debugLogf("[DNS] Cross-zone CNAME found: %s in zone %s", current, currentZoneName)
				}
			}
		}
		if len(cnameRecords) == 0 {
			break
		}

		cname := cnameRecords[0]
		records = append(records, cname)
		debugLogf("[DNS] CNAME found: %s -> %s", current, cname.Content)

		target := cname.Content
		if !strings.HasSuffix(target, ".") {
			target = target + "."
		}

		targetRecords, viaGSLB, err := h.resolveTargetRecords(zoneID, target, qtype, clientIP)
		if err != nil {
			return records, false, err
		}
		if len(targetRecords) > 0 {
			debugLogf("[DNS] CNAME target records found: %d records (viaGSLB: %v)", len(targetRecords), viaGSLB)
			records = append(records, targetRecords...)
			resolvedViaGSLB = viaGSLB
			return records, resolvedViaGSLB, nil
		}

		current = target
	}

	return records, resolvedViaGSLB, nil
}

// resolveTargetRecords은 CNAME 타겟의 A/AAAA 레코드를 GSLB 또는 로컬 레코드에서 조회합니다.
// 반환값: records, resolvedViaGSLB, error
func (h *Handler) resolveTargetRecords(zoneID int64, target, qtype string, clientIP net.IP) ([]*model.Record, bool, error) {
	// 1. GSLB 도메인 조회
	if h.gslbEngine != nil {
		ips, _, err := h.gslbEngine.Resolve(target, qtype, clientIP)
		if err == nil && len(ips) > 0 {
			debugLogf("[DNS] CNAME target is GSLB domain: %s, resolved %d IPs", target, len(ips))
			records := make([]*model.Record, 0, len(ips))
			for _, ip := range ips {
				if ip == nil {
					continue
				}
				switch qtype {
				case "A":
					if ip4 := ip.To4(); ip4 != nil {
						records = append(records, &model.Record{
							Name:    target,
							Type:    "A",
							Content: ip4.String(),
							TTL:     60,
							Enabled: true,
						})
					}
				case "AAAA":
					if ip.To4() == nil {
						records = append(records, &model.Record{
							Name:    target,
							Type:    "AAAA",
							Content: ip.String(),
							TTL:     60,
							Enabled: true,
						})
					}
				}
			}
			if len(records) > 0 {
				return records, true, nil
			}
		}
	}

	// 2. 동일 Zone 내 레코드 조회
	records, err := h.recordStorage.GetRecordsByNameAndZone(zoneID, target, qtype)
	if err != nil {
		return nil, false, err
	}
	if len(records) > 0 {
		return records, false, nil
	}

	// 3. 크로스 Zone 조회 - 타겟이 다른 Zone에 속할 수 있음
	targetZoneName := h.extractDomain(target)
	targetZone, err := h.zoneStorage.GetZoneByName(targetZoneName)
	if err == nil && targetZone != nil && targetZone.ID != zoneID {
		records, err = h.recordStorage.GetRecordsByNameAndZone(targetZone.ID, target, qtype)
		if err != nil {
			return nil, false, err
		}
		if len(records) > 0 {
			debugLogf("[DNS] Cross-zone resolution: found %s record for %s in zone %s", qtype, target, targetZoneName)
			return records, false, nil
		}
	}

	// 4. 로컬에 없으면 업스트림 포워딩으로 해석
	if h.resolver != nil {
		msg := new(dns.Msg)
		msg.SetQuestion(dns.Fqdn(target), dns.StringToType[qtype])
		msg.RecursionDesired = true

		resp, err := h.resolver.Forward(msg)
		if err == nil && len(resp.Answer) > 0 {
			records := make([]*model.Record, 0, len(resp.Answer))
			for _, rr := range resp.Answer {
				switch v := rr.(type) {
				case *dns.A:
					if qtype == "A" {
						records = append(records, &model.Record{
							Name:    target,
							Type:    "A",
							Content: v.A.String(),
							TTL:     int64(v.Hdr.Ttl),
							Enabled: true,
						})
					}
				case *dns.AAAA:
					if qtype == "AAAA" {
						records = append(records, &model.Record{
							Name:    target,
							Type:    "AAAA",
							Content: v.AAAA.String(),
							TTL:     int64(v.Hdr.Ttl),
							Enabled: true,
						})
					}
				}
			}
			if len(records) > 0 {
				debugLogf("[DNS] Upstream resolution: found %d %s record(s) for %s", len(records), qtype, target)
				return records, false, nil
			}
		}
	}

	return nil, false, nil
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
		target := record.Content
		if !strings.HasSuffix(target, ".") {
			target = target + "."
		}
		return &dns.CNAME{
			Hdr:    header,
			Target: target,
		}

	case "MX":
		mx := record.Content
		if !strings.HasSuffix(mx, ".") {
			mx += "."
		}
		return &dns.MX{
			Hdr:        header,
			Preference: uint16(record.Priority),
			Mx:         mx,
		}

	case "TXT":
		return &dns.TXT{
			Hdr: header,
			Txt: []string{record.Content},
		}

	case "NS":
		ns := record.Content
		if !strings.HasSuffix(ns, ".") {
			ns += "."
		}
		return &dns.NS{
			Hdr: header,
			Ns:  ns,
		}

	case "SOA":
		// SOA 레코드는 Zone 정보에서 생성되어야 하지만
		// Record 테이블에도 저장될 수 있음
		// Content 포맷: "mname rname serial refresh retry expire minimum"
		parts := strings.Fields(record.Content)
		if len(parts) >= 7 {
			ns := parts[0]
			if !strings.HasSuffix(ns, ".") {
				ns += "."
			}
			mbox := parts[1]
			if !strings.HasSuffix(mbox, ".") {
				mbox += "."
			}
			return &dns.SOA{
				Hdr:     header,
				Ns:      ns,
				Mbox:    mbox,
				Serial:  parseUint32(parts[2]),
				Refresh: parseUint32(parts[3]),
				Retry:   parseUint32(parts[4]),
				Expire:  parseUint32(parts[5]),
				Minttl:  parseUint32(parts[6]),
			}
		}

	case "SRV":
		parts := strings.Fields(record.Content)
		priority := uint16(record.Priority)
		var weight, port uint16
		var target string
		switch {
		case len(parts) >= 4:
			priority = parseUint16(parts[0])
			weight = parseUint16(parts[1])
			port = parseUint16(parts[2])
			target = parts[3]
		case len(parts) >= 3:
			weight = parseUint16(parts[0])
			port = parseUint16(parts[1])
			target = parts[2]
		default:
			return nil
		}
		if !strings.HasSuffix(target, ".") {
			target += "."
		}
		return &dns.SRV{
			Hdr:      header,
			Priority: priority,
			Weight:   weight,
			Port:     port,
			Target:   target,
		}

	case "PTR":
		ptr := record.Content
		if !strings.HasSuffix(ptr, ".") {
			ptr += "."
		}
		return &dns.PTR{
			Hdr: header,
			Ptr: ptr,
		}

	case "CAA":
		parts := strings.Fields(record.Content)
		if len(parts) < 3 {
			return nil
		}
		return &dns.CAA{
			Hdr:   header,
			Flag:  uint8(parseUint16(parts[0])),
			Tag:   parts[1],
			Value: strings.Join(parts[2:], " "),
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
	debugLogf("[DNS] Prefetch triggered: %s %s", domain, qtype)

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
		h.GetCache().Set(domain, qtype, answer.Answer, minTTL, false)
		debugLogf("[DNS] Prefetch completed: %s %s (%d records)", domain, qtype, len(records))
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

			h.GetCache().Set(domain, qtype, upstreamResp.Answer, minTTL, false)
			debugLogf("[DNS] Prefetch from upstream completed: %s %s", domain, qtype)
		}
	}
}

// parseUint32는 문자열을 uint32로 파싱합니다
func parseUint32(s string) uint32 {
	var result uint32
	_, _ = fmt.Sscanf(s, "%d", &result)
	return result
}

func parseUint16(s string) uint16 {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0
	}
	return uint16(n)
}

// GetCache는 L1 캐시를 반환합니다 (테스트용)
func (h *Handler) GetCache() *DNSCache {
	h.cacheMu.RLock()
	defer h.cacheMu.RUnlock()
	return h.cache
}

// ReconfigureCache applies new cache settings to L1 cache
func (h *Handler) ReconfigureCache(settings *model.CacheSettings) {
	if settings == nil {
		return
	}
	cache := NewDNSCache(settings.MaxSize, settings.DefaultTTL, settings.NegativeTTL, settings.PrefetchTrigger)
	cache.SetPrefetchFunc(h.handlePrefetch)
	h.cacheMu.Lock()
	oldCache := h.cache
	h.cache = cache
	h.cacheMu.Unlock()
	if oldCache != nil {
		oldCache.Stop()
	}
}
