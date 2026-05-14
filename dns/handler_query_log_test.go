package dns

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tcpMockResponseWriter struct {
	mockResponseWriter
}

func (m *tcpMockResponseWriter) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP(m.remoteIP), Port: 5353}
}

func TestSummarizeAnswers(t *testing.T) {
	rrs := []dns.RR{
		&dns.A{Hdr: dns.RR_Header{Name: "a.example.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("192.0.2.1")},
		&dns.AAAA{Hdr: dns.RR_Header{Name: "aaaa.example.", Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60}, AAAA: net.ParseIP("2001:db8::1")},
		&dns.CNAME{Hdr: dns.RR_Header{Name: "c.example.", Rrtype: dns.TypeCNAME, Class: dns.ClassINET, Ttl: 60}, Target: "target.example."},
		&dns.MX{Hdr: dns.RR_Header{Name: "mx.example.", Rrtype: dns.TypeMX, Class: dns.ClassINET, Ttl: 60}, Preference: 10, Mx: "mail.example."},
		&dns.TXT{Hdr: dns.RR_Header{Name: "txt.example.", Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60}, Txt: []string{"hello", "world"}},
		&dns.NS{Hdr: dns.RR_Header{Name: "example.", Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 60}, Ns: "ns.example."},
		&dns.SOA{Hdr: dns.RR_Header{Name: "example.", Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 60}, Ns: "ns.example."},
		&dns.SRV{Hdr: dns.RR_Header{Name: "_sip._tcp.example.", Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: 60}, Priority: 1, Weight: 2, Port: 5060, Target: "sip.example."},
		&dns.PTR{Hdr: dns.RR_Header{Name: "1.2.0.192.in-addr.arpa.", Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 60}, Ptr: "ptr.example."},
		&dns.CAA{Hdr: dns.RR_Header{Name: "example.", Rrtype: dns.TypeCAA, Class: dns.ClassINET, Ttl: 60}, Tag: "issue", Value: "ca.example"},
	}

	summary := summarizeAnswers(rrs)
	assert.Contains(t, summary, "192.0.2.1")
	assert.Contains(t, summary, "2001:db8::1")
	assert.Contains(t, summary, "CNAME:target.example.")
	assert.Contains(t, summary, "MX:10:mail.example.")
	assert.Contains(t, summary, "TXT:hello world")
	assert.Contains(t, summary, "NS:ns.example.")
	assert.Contains(t, summary, "SOA:ns.example.")
	assert.Contains(t, summary, "SRV:1:2:5060:sip.example.")
	assert.Contains(t, summary, "PTR:ptr.example.")
	assert.Contains(t, summary, "example.\t60\tIN\tCAA")
	assert.Empty(t, summarizeAnswers(nil))
}

func TestHandlerLogQueryRecordsMetadata(t *testing.T) {
	fake := &queryLogBatchWriterFunc{}
	writer := NewQueryLogWriter(fake, time.Hour, 10)
	require.NotNil(t, writer)
	defer writer.Stop()

	handler := &Handler{queryLogWriter: writer}
	req := new(dns.Msg)
	req.SetQuestion("Example.COM.", dns.TypeA)
	req.SetEdns0(4096, false)
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Rcode = dns.RcodeSuccess
	resp.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.ParseIP("192.0.2.10")}}

	handler.logQuery(&tcpMockResponseWriter{mockResponseWriter: mockResponseWriter{remoteIP: "198.51.100.10"}}, req, resp, "zone", time.Now().Add(-time.Millisecond))
	writer.flush()

	require.Equal(t, 1, fake.batchCount())
	entry := fake.batches[0][0]
	assert.Equal(t, "198.51.100.10", entry.ClientIP)
	assert.Equal(t, "example.com.", entry.Domain)
	assert.Equal(t, "A", entry.QueryType)
	assert.Equal(t, "NOERROR", entry.ResponseCode)
	assert.Equal(t, "zone", entry.ResponseSource)
	assert.Equal(t, "tcp", entry.Protocol)
	assert.True(t, entry.EDNSPresent)
	assert.Equal(t, 4096, entry.EDNSBufferSize)
	assert.Contains(t, entry.ResponseData, "192.0.2.10")
	assert.Greater(t, entry.ResponseSize, 0)
	assert.WithinDuration(t, time.Now().UTC(), entry.Timestamp, time.Second)
}

func TestHandlerLogQuerySkipsWithoutWriterOrQuestion(t *testing.T) {
	handler := &Handler{}
	req := new(dns.Msg)
	resp := new(dns.Msg)
	handler.logQuery(newMockWriter("192.0.2.10"), req, resp, "error", time.Now())

	fake := &queryLogBatchWriterFunc{}
	writer := NewQueryLogWriter(fake, time.Hour, 10)
	require.NotNil(t, writer)
	defer writer.Stop()
	handler.queryLogWriter = writer
	handler.logQuery(newMockWriter("192.0.2.10"), req, resp, "error", time.Now())
	writer.flush()
	assert.Equal(t, 0, fake.batchCount())
}

func TestHandlerLogQueryUDPRemoteAddrFallback(t *testing.T) {
	fake := &queryLogBatchWriterFunc{}
	writer := NewQueryLogWriter(fake, time.Hour, 10)
	require.NotNil(t, writer)
	defer writer.Stop()

	handler := &Handler{queryLogWriter: writer}
	req := new(dns.Msg)
	req.SetQuestion("udp.example.", dns.TypeAAAA)
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.Rcode = dns.RcodeNameError

	handler.logQuery(newMockWriter("203.0.113.5"), req, resp, "cache", time.Now())
	writer.flush()

	require.Equal(t, 1, fake.batchCount())
	entry := fake.batches[0][0]
	assert.Equal(t, "udp", entry.Protocol)
	assert.Equal(t, "203.0.113.5", entry.ClientIP)
	assert.Equal(t, "NXDOMAIN", entry.ResponseCode)
	assert.Equal(t, "AAAA", entry.QueryType)
	assert.Equal(t, "udp.example.", entry.Domain)
}

func TestDebugLogfEnabledBranch(t *testing.T) {
	old := dnsDebugLogsEnabled
	dnsDebugLogsEnabled = true
	defer func() { dnsDebugLogsEnabled = old }()

	debugLogf("[DNS] debug test %s", "enabled")
}
