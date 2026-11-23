// Package analytics ingests Caddy JSON access logs and computes privacy-safe
// web traffic metrics (DAU/WAU/MAU, PV, bandwidth, latency, and dimension tables).
package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"time"

	"deploycontrol/internal/config"
)

// Event is one classified, privacy-hashed access log entry.
type Event struct {
	Site        string
	Day         string
	Hour        string
	UnixSec     int64
	VisitorHash string
	Path        string
	Referrer    string
	Browser     string
	OS          string
	Device      string
	StatusCode  string
	Status      int
	Bytes       int64
	DurationMS  float64
	ClientIP    string
	UserAgent   string
	Threat      string
	IsBot       bool
	BotName     string
	IsAsset     bool
	IsPV        bool
	BotOnly     bool
}

// Matcher maps Caddy access log entries onto configured sites and applies
// exclusion and classification rules.
type Matcher struct {
	hostToSite      map[string]string
	excludePaths    []string
	excludeAddrs    []netip.Addr
	excludeCIDRs    []netip.Prefix
	allowAddrs      []netip.Addr
	allowCIDRs      []netip.Prefix
	extraHoneypot   []string
	excludePHPSites map[string]bool
	shieldEnabled   bool
	banScanners     bool
	ignoreBots      bool
	loc             *time.Location
}

// NewMatcher builds a Matcher from cfg.
func NewMatcher(cfg *config.Config) *Matcher {
	m := &Matcher{
		hostToSite:      make(map[string]string),
		excludePaths:    append([]string(nil), cfg.Analytics.ExcludePaths...),
		extraHoneypot:   append([]string(nil), cfg.Analytics.Shield.HoneypotPaths...),
		excludePHPSites: make(map[string]bool),
		shieldEnabled:   cfg.Analytics.Shield.Enabled,
		banScanners:     cfg.Analytics.Shield.ShouldBanScanners(),
		ignoreBots:      cfg.Analytics.ShouldIgnoreBots(),
		loc:             cfg.AnalyticsLocation(),
	}
	for _, s := range cfg.Sites {
		for _, h := range s.AnalyticsHosts() {
			if _, exists := m.hostToSite[h]; !exists {
				m.hostToSite[h] = s.Name
			}
		}
	}
	for _, site := range cfg.Analytics.Shield.ExcludePHPSites {
		m.excludePHPSites[site] = true
	}
	m.excludeAddrs, m.excludeCIDRs = parseIPRules(cfg.Analytics.ExcludeIPs)
	m.allowAddrs, m.allowCIDRs = parseIPRules(cfg.Analytics.Shield.AllowIPs)
	return m
}

func parseIPRules(list []string) ([]netip.Addr, []netip.Prefix) {
	var addrs []netip.Addr
	var cidrs []netip.Prefix
	for _, raw := range list {
		if p, err := netip.ParsePrefix(raw); err == nil {
			cidrs = append(cidrs, p)
		} else if a, err := netip.ParseAddr(raw); err == nil {
			addrs = append(addrs, a.Unmap())
		}
	}
	return addrs, cidrs
}

// SiteForHost resolves a request host to a configured site name.
func (m *Matcher) SiteForHost(host string) (string, bool) {
	h := normalizeHost(host)
	site, ok := m.hostToSite[h]
	return site, ok
}

func matchIPRule(ipStr string, addrs []netip.Addr, cidrs []netip.Prefix) bool {
	if ipStr == "" || (len(addrs) == 0 && len(cidrs) == 0) {
		return false
	}
	addr, err := netip.ParseAddr(ipStr)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, a := range addrs {
		if a == addr {
			return true
		}
	}
	for _, p := range cidrs {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func (m *Matcher) isExcludedIP(ipStr string) bool {
	return matchIPRule(ipStr, m.excludeAddrs, m.excludeCIDRs)
}

var protectedCIDRs = mustParsePrefixes([]string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
})

func mustParsePrefixes(list []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}

func unmapPrefix(p netip.Prefix) netip.Prefix {
	if !p.IsValid() {
		return p
	}
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		return netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96).Masked()
	}
	return p.Masked()
}

// IsProtectedIP reports whether ipStr must never be banned (loopback, private,
// link-local, unspecified, exclude_ips, or shield.allow_ips).
func (m *Matcher) IsProtectedIP(ipStr string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(ipStr))
	if err != nil {
		return true
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsUnspecified() {
		return true
	}
	return matchIPRule(addr.String(), m.excludeAddrs, m.excludeCIDRs) ||
		matchIPRule(addr.String(), m.allowAddrs, m.allowCIDRs)
}

// IsProtectedPrefix reports whether prefix overlaps any reserved, excluded, or allowlisted IP/CIDR.
func (m *Matcher) IsProtectedPrefix(p netip.Prefix) bool {
	p = unmapPrefix(p)
	if !p.IsValid() || p.Addr().IsUnspecified() {
		return true
	}
	for _, rp := range protectedCIDRs {
		if p.Overlaps(rp) {
			return true
		}
	}
	for _, a := range m.excludeAddrs {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	for _, rp := range m.excludeCIDRs {
		if p.Overlaps(unmapPrefix(rp)) {
			return true
		}
	}
	for _, a := range m.allowAddrs {
		if p.Contains(a.Unmap()) {
			return true
		}
	}
	for _, rp := range m.allowCIDRs {
		if p.Overlaps(unmapPrefix(rp)) {
			return true
		}
	}
	return false
}

func (m *Matcher) isExcludedPath(p string) bool {
	for _, prefix := range m.excludePaths {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

type caddyLogLine struct {
	TS       json.RawMessage `json:"ts"`
	Duration float64         `json:"duration"`
	Size     int64           `json:"size"`
	Status   int             `json:"status"`
	Request  struct {
		RemoteIP string              `json:"remote_ip"`
		ClientIP string              `json:"client_ip"`
		Method   string              `json:"method"`
		Host     string              `json:"host"`
		URI      string              `json:"uri"`
		Headers  map[string][]string `json:"headers"`
	} `json:"request"`
	RespHeaders map[string][]string `json:"resp_headers"`
}

// ParseLine parses one Caddy JSON access log line. ok is false if the line is
// malformed, does not match a configured site, or matches an exclusion rule.
func (m *Matcher) ParseLine(line []byte, salt string) (Event, bool) {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return Event{}, false
	}
	var raw caddyLogLine
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return Event{}, false
	}
	if raw.Request.Host == "" && raw.Status == 0 {
		return Event{}, false
	}
	site, ok := m.SiteForHost(raw.Request.Host)
	if !ok {
		return Event{}, false
	}

	ip := extractIP(raw.Request.ClientIP)
	if ip == "" {
		ip = extractIP(raw.Request.RemoteIP)
	}
	if m.isExcludedIP(ip) {
		return Event{}, false
	}

	reqPath := normalizePath(raw.Request.URI)
	if m.isExcludedPath(reqPath) {
		return Event{}, false
	}

	ts, ok := parseCaddyTS(raw.TS)
	if !ok {
		ts = time.Now()
	}
	localTS := ts.In(m.loc)

	ua := headerFirst(raw.Request.Headers, "User-Agent")
	ref := m.normalizeReferrer(headerFirst(raw.Request.Headers, "Referer"), site, raw.Request.Host)
	contentType := strings.ToLower(headerFirst(raw.RespHeaders, "Content-Type"))

	isBot, botName := classifyBot(ua)
	isAsset := detectAsset(reqPath, contentType)
	method := strings.ToUpper(strings.TrimSpace(raw.Request.Method))
	botOnly := isBot && m.ignoreBots

	var threat string
	if m.shieldEnabled {
		threat = m.detectThreat(site, reqPath, ua, botName)
		if threat != "" {
			if !isBot {
				isBot = true
				botName = "Honeypot Probe"
			}
			botOnly = m.ignoreBots
			if ip == "" || m.IsProtectedIP(ip) {
				threat = ""
			}
		}
	}

	// PV counts human GET requests for non-asset, page-like resources returning 200/206/304.
	isPV := !botOnly && method == "GET" && !isAsset && !isNonHTMLContentType(contentType) &&
		(raw.Status == 200 || raw.Status == 206 || raw.Status == 304)

	browser, osName, device := parseUserAgent(ua)
	durMS := raw.Duration * 1000.0
	if durMS < 0 {
		durMS = 0
	}
	sizeBytes := raw.Size
	if sizeBytes < 0 {
		sizeBytes = 0
	}

	return Event{
		Site:        site,
		Day:         localTS.Format("2006-01-02"),
		Hour:        localTS.Format("2006-01-02T15"),
		UnixSec:     ts.Unix(),
		VisitorHash: hashVisitor(salt, ip, ua),
		ClientIP:    ip,
		UserAgent:   ua,
		Threat:      threat,
		Path:        reqPath,
		Referrer:    ref,
		Browser:     browser,
		OS:          osName,
		Device:      device,
		StatusCode:  statusLabel(raw.Status),
		Status:      raw.Status,
		Bytes:       sizeBytes,
		DurationMS:  durMS,
		IsBot:       isBot,
		BotName:     botName,
		IsAsset:     isAsset,
		IsPV:        isPV,
		BotOnly:     botOnly,
	}, true
}

func parseCaddyTS(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil && f > 0 {
		// Values > 1e12 are likely milliseconds; Caddy default is seconds float64.
		if f > 1e12 {
			f /= 1000.0
		}
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC(), true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999 -0700 MST", "2006/01/02 15:04:05"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

func extractIP(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	if addr, err := netip.ParseAddr(s); err == nil {
		return addr.Unmap().String()
	}
	return s
}

func normalizeHost(raw string) string {
	h := strings.ToLower(strings.TrimSpace(raw))
	if h == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.TrimSuffix(h, ".")
}

func normalizePath(uri string) string {
	u := strings.TrimSpace(uri)
	if u == "" {
		return "/"
	}
	if idx := strings.IndexByte(u, '?'); idx >= 0 {
		u = u[:idx]
	}
	if idx := strings.IndexByte(u, '#'); idx >= 0 {
		u = u[:idx]
	}
	if u == "" {
		return "/"
	}
	if !strings.HasPrefix(u, "/") {
		if parsed, err := url.Parse(u); err == nil && parsed.Path != "" {
			u = parsed.Path
		} else {
			u = "/" + u
		}
	}
	cleaned := path.Clean(u)
	if cleaned == "." {
		cleaned = "/"
	}
	// Preserve trailing slash for non-root paths if original had one so /blog and /blog/ match cleanly?
	// Actually collapsing trailing slash (except "/") avoids duplicate rows for /about and /about/.
	if len(cleaned) > 256 {
		cleaned = cleaned[:256]
	}
	return cleaned
}

func (m *Matcher) normalizeReferrer(raw, site, siteHost string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "(direct)"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(direct)"
	}
	refHost := normalizeHost(u.Host)
	if refHost == "" || refHost == normalizeHost(siteHost) {
		return "(direct)"
	}
	if refSite, ok := m.SiteForHost(refHost); ok && refSite == site {
		return "(direct)"
	}
	if len(refHost) > 128 {
		refHost = refHost[:128]
	}
	return refHost
}

func headerFirst(headers map[string][]string, name string) string {
	if len(headers) == 0 {
		return ""
	}
	if vals, ok := headers[name]; ok && len(vals) > 0 {
		return vals[0]
	}
	for k, vals := range headers {
		if strings.EqualFold(k, name) && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

func hashVisitor(salt, ip, ua string) string {
	h := sha256.New()
	h.Write([]byte(salt))
	h.Write([]byte("|"))
	h.Write([]byte(ip))
	h.Write([]byte("|"))
	h.Write([]byte(ua))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16])
}

func statusLabel(code int) string {
	if code <= 0 {
		return "unknown"
	}
	// Avoid strconv import overhead for 3-digit codes.
	return string([]byte{
		byte('0' + (code/100)%10),
		byte('0' + (code/10)%10),
		byte('0' + code%10),
	})
}

type botRule struct {
	substr string
	label  string
}

var botRules = []botRule{
	// AI crawlers
	{"gptbot", "GPTBot (AI)"},
	{"oai-searchbot", "OAI-SearchBot (AI)"},
	{"chatgpt-user", "ChatGPT-User (AI)"},
	{"claudebot", "ClaudeBot (AI)"},
	{"anthropic-ai", "Anthropic-AI (AI)"},
	{"claude-web", "Claude-Web (AI)"},
	{"bytespider", "Bytespider (AI)"},
	{"ccbot", "CCBot (AI)"},
	{"perplexitybot", "PerplexityBot (AI)"},
	{"amazonbot", "Amazonbot (AI)"},
	{"meta-externalagent", "Meta-ExternalAgent (AI)"},
	{"meta-webindexer", "Meta-WebIndexer (AI)"},
	{"elevenlabscrawler", "ElevenlabsCrawler (AI)"},
	// Search engines
	{"googlebot", "Googlebot (Search)"},
	{"google-inspectiontool", "Google-Inspection (Search)"},
	{"adsbot-google", "AdsBot-Google (Search)"},
	{"mediapartners", "AdSense-Bot (Search)"},
	{"bingbot", "Bingbot (Search)"},
	{"bingpreview", "BingPreview (Search)"},
	{"msnbot", "MSNBot (Search)"},
	{"baiduspider", "Baiduspider (Search)"},
	{"yandex", "YandexBot (Search)"},
	{"duckduckbot", "DuckDuckBot (Search)"},
	{"applebot", "Applebot (Search)"},
	{"petalbot", "PetalBot (Search)"},
	{"sogou", "SogouSpider (Search)"},
	{"360spider", "360Spider (Search)"},
	{"slurp", "Yahoo! Slurp (Search)"},
	// SEO crawlers
	{"ahrefs", "AhrefsBot (SEO)"},
	{"semrush", "SemrushBot (SEO)"},
	{"mj12bot", "MJ12bot (SEO)"},
	{"dotbot", "DotBot (SEO)"},
	{"barkrowler", "Barkrowler (SEO)"},
	// Social / link previews
	{"facebookexternalhit", "Facebook Preview"},
	{"twitterbot", "Twitterbot (Preview)"},
	{"telegrambot", "TelegramBot (Preview)"},
	{"discordbot", "Discordbot (Preview)"},
	{"slackbot", "Slackbot (Preview)"},
	{"whatsapp", "WhatsApp (Preview)"},
	{"linkedinbot", "LinkedInBot (Preview)"},
	{"embedly", "Embedly (Preview)"},
	{"quora link preview", "Quora Preview"},
	// Monitoring & headless perf
	{"uptimerobot", "UptimeRobot (Monitor)"},
	{"pingdom", "Pingdom (Monitor)"},
	{"lighthouse", "Lighthouse (Audit)"},
	{"pagespeed", "PageSpeed (Audit)"},
	{"headlesschrome", "HeadlessChrome"},
	{"prometheus", "Prometheus (Monitor)"},
	{"nagios", "Nagios (Monitor)"},
	{"zabbix", "Zabbix (Monitor)"},
	{"check_http", "check_http (Monitor)"},
	{"monitoring", "Monitor Probe"},
	// CLI / HTTP libraries
	{"curl/", "curl (CLI)"},
	{"wget/", "wget (CLI)"},
	{"python-requests", "Python-Requests (HTTP)"},
	{"python-urllib", "Python-Urllib (HTTP)"},
	{"httpx", "HTTPX (HTTP)"},
	{"aiohttp", "aiohttp (HTTP)"},
	{"go-http-client", "Go-http-client (HTTP)"},
	{"axios/", "Axios (HTTP)"},
	{"node-fetch", "node-fetch (HTTP)"},
	{"undici", "Undici (HTTP)"},
	{"okhttp", "OkHttp (HTTP)"},
	{"java/", "Java HTTP Client"},
	{"libwww-perl", "libwww-perl (HTTP)"},
	// Security scanners
	{"nuclei", "Nuclei (Scanner)"},
	{"nikto", "Nikto (Scanner)"},
	{"sqlmap", "sqlmap (Scanner)"},
	{"nmap", "Nmap (Scanner)"},
	{"masscan", "Masscan (Scanner)"},
	{"zgrab", "ZGrab (Scanner)"},
}

func classifyBot(ua string) (bool, string) {
	trimmed := strings.TrimSpace(ua)
	if trimmed == "" {
		return true, "Empty UA"
	}
	l := strings.ToLower(trimmed)
	for _, r := range botRules {
		if strings.Contains(l, r.substr) {
			return true, r.label
		}
	}
	matched := false
	for _, marker := range []string{"bot", "crawler", "spider"} {
		if strings.Contains(l, marker) {
			matched = true
			if tok := extractBotToken(trimmed, marker); tok != "" {
				return true, tok
			}
		}
	}
	if matched {
		return true, "Other Bot"
	}
	return false, ""
}

func extractBotToken(ua, marker string) string {
	fields := strings.FieldsFunc(ua, func(r rune) bool {
		return r == ' ' || r == ';' || r == '(' || r == ')' || r == ','
	})
	for _, f := range fields {
		lf := strings.ToLower(f)
		if strings.HasPrefix(lf, "http://") || strings.HasPrefix(lf, "https://") ||
			strings.HasPrefix(lf, "+http://") || strings.HasPrefix(lf, "+https://") {
			continue
		}
		if strings.Contains(lf, marker) {
			if slash := strings.IndexByte(f, '/'); slash > 0 {
				f = f[:slash]
			}
			f = strings.Trim(f, "+:;\"'")
			if len(f) > 48 {
				f = f[:48]
			}
			if f != "" {
				return f
			}
		}
	}
	return ""
}

var assetExts = map[string]bool{
	".css": true, ".js": true, ".mjs": true, ".cjs": true, ".map": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".avif": true, ".svg": true, ".ico": true, ".bmp": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".wasm": true, ".mp4": true, ".webm": true, ".mp3": true, ".ogg": true,
	".wav": true, ".zip": true, ".tar": true, ".gz": true, ".br": true,
	".bz2": true, ".xz": true, ".7z": true, ".apk": true, ".ipa": true,
	".webmanifest": true,
}

var assetPrefixes = []string{
	"/_astro/", "/_nuxt/", "/_next/static/", "/_next/image",
	"/assets/", "/static/", "/favicon",
}

var assetExactPaths = map[string]bool{
	"/robots.txt":  true,
	"/sitemap.xml": true,
	"/sitemap-index.xml": true,
	"/ads.txt":     true,
	"/manifest.json": true,
}

func detectAsset(reqPath, contentType string) bool {
	lp := strings.ToLower(reqPath)
	if assetExactPaths[lp] {
		return true
	}
	for _, p := range assetPrefixes {
		if strings.HasPrefix(lp, p) {
			return true
		}
	}
	if ext := path.Ext(lp); ext != "" && assetExts[ext] {
		return true
	}
	if contentType != "" {
		if strings.HasPrefix(contentType, "image/") ||
			strings.HasPrefix(contentType, "font/") ||
			strings.HasPrefix(contentType, "audio/") ||
			strings.HasPrefix(contentType, "video/") ||
			strings.Contains(contentType, "text/css") ||
			strings.Contains(contentType, "javascript") ||
			strings.Contains(contentType, "application/wasm") {
			return true
		}
	}
	return false
}

// isNonHTMLContentType returns true when an explicit Content-Type header shows
// the response is an API payload or feed rather than an HTML page. Empty
// Content-Type (common on 304 Not Modified) returns false.
func isNonHTMLContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml") {
		return false
	}
	return strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "application/xml") ||
		strings.Contains(contentType, "text/xml") ||
		strings.Contains(contentType, "application/rss") ||
		strings.Contains(contentType, "application/atom") ||
		strings.Contains(contentType, "application/octet-stream")
}

func parseUserAgent(ua string) (browser, osName, device string) {
	if strings.TrimSpace(ua) == "" {
		return "Other", "Other", "Desktop"
	}
	l := strings.ToLower(ua)

	switch {
	case strings.Contains(l, "edg/") || strings.Contains(l, "edge/") || strings.Contains(l, "edga/") || strings.Contains(l, "edgios/"):
		browser = "Edge"
	case strings.Contains(l, "opr/") || strings.Contains(l, "opera"):
		browser = "Opera"
	case strings.Contains(l, "firefox/") || strings.Contains(l, "fxios/"):
		browser = "Firefox"
	case strings.Contains(l, "chrome/") || strings.Contains(l, "crios/"):
		browser = "Chrome"
	case strings.Contains(l, "safari/"):
		browser = "Safari"
	default:
		browser = "Other"
	}

	switch {
	case strings.Contains(l, "iphone") || strings.Contains(l, "ipad") || strings.Contains(l, "ipod"):
		osName = "iOS"
	case strings.Contains(l, "android"):
		osName = "Android"
	case strings.Contains(l, "windows"):
		osName = "Windows"
	case strings.Contains(l, "mac os x") || strings.Contains(l, "macintosh"):
		osName = "macOS"
	case strings.Contains(l, "linux") || strings.Contains(l, "x11"):
		osName = "Linux"
	default:
		osName = "Other"
	}

	switch {
	case strings.Contains(l, "ipad") || strings.Contains(l, "tablet"):
		device = "Tablet"
	case strings.Contains(l, "mobile") || strings.Contains(l, "iphone") || strings.Contains(l, "ipod"):
		device = "Mobile"
	default:
		device = "Desktop"
	}
	return browser, osName, device
}
