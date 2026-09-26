package collector

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/VibhuviOiO/openldap-exporter/internal/config"
	"github.com/VibhuviOiO/openldap-exporter/internal/csn"
)

// result is everything one scrape of one target learned. Every phase records
// its own error so a partial scrape still exports what it could read.
type result struct {
	target config.Target
	up     bool

	errs map[string]error // phase -> error

	dialSeconds float64
	bindSeconds float64
	searchSecs  map[string]float64 // phase or entry name -> seconds

	// root DSE
	namingContexts []string
	vendorVersion  string

	// cn=Monitor
	mon *monitor

	// contextCSN per suffix
	csn map[string]csn.Set

	// cn=config
	cfg *serverConfig

	// configAge is how old the cn=config data is. Zero when it was read during
	// this scrape; non-zero when served from the cache.
	configAge time.Duration

	// entry counts, keyed by config.EntryCount.Name
	entries map[string]float64

	// TLS
	tlsNotAfter time.Time
	tlsIssuer   string

	// resolved addresses of this target, for provider-side link matching
	addrs []string
}

// serverConfig is what we read from cn=config.
type serverConfig struct {
	serverID  int
	serverIDs []string // raw olcServerID values, for info metric
	logLevel  string
	databases []dbConfig
	syncprov  []syncprovConfig
	readable  bool
}

type dbConfig struct {
	dn            string
	suffix        string
	backend       string // mdb, hdb, ...
	multiProvider bool
	mirrorMode    bool
	readOnly      bool
	maxSize       float64
	consumers     []consumer
}

type syncprovConfig struct {
	dbDN       string
	checkpoint string
	sessionlog float64
}

// consumer is one parsed olcSyncRepl statement.
//
// SECURITY: an olcSyncRepl value looks like
//
//	rid=201 provider=ldap://host:389 binddn="cn=Manager,..." bindmethod=simple
//	credentials=s3cret searchbase="..." type=refreshAndPersist retry="..." keepalive=...
//
// and OpenLDAP stores `credentials` (and often `binddn`) in cleartext inside
// that single attribute value. This struct is what becomes Prometheus labels
// (openldap_syncrepl_consumer_info and friends), which end up in metrics,
// dashboards, and anything scraping /metrics — so it deliberately has no
// field for `credentials` or `binddn`, and parseSyncrepl below must never add
// one. If you're adding a field here, don't copy kv["credentials"] or
// kv["binddn"] into it; TestParseSyncreplNeverExposesCredentials enforces
// this.
type consumer struct {
	rid          string
	provider     string // full URL as written
	providerHost string // host only, lower-case
	searchbase   string
	typ          string // refreshAndPersist | refreshOnly
	retry        string
	retryForever bool
	keepalive    string
	interval     string
	filter       string
	bindmethod   string
}

// scrape performs one full scrape of one target within ctx's deadline.
func scrape(ctx context.Context, t config.Target, c *config.Config, cache *configCache) *result {
	r := &result{
		target:     t,
		errs:       map[string]error{},
		searchSecs: map[string]float64{},
		csn:        map[string]csn.Set{},
		entries:    map[string]float64{},
	}
	deadline, ok := ctx.Deadline()
	timeout := c.ScrapeTimeout
	if ok {
		timeout = time.Until(deadline)
	}

	// Resolve our own address regardless of connect success; the link check on
	// other nodes needs it.
	if host := hostOf(t.URI); host != "" {
		if ips, err := net.DefaultResolver.LookupHost(ctx, host); err == nil {
			r.addrs = ips
		} else if ip := net.ParseIP(host); ip != nil {
			r.addrs = []string{host}
		}
	}

	conn, err := dial(t, timeout, r)
	if err != nil {
		r.errs["dial"] = err
		return r
	}
	defer conn.Close()
	conn.SetTimeout(timeout)

	if t.BindDN != "" {
		start := time.Now()
		if err := conn.Bind(t.BindDN, t.ResolvedPassword); err != nil {
			r.errs["bind"] = err
			return r
		}
		r.bindSeconds = time.Since(start).Seconds()
	}
	r.up = true

	if state, ok := conn.TLSConnectionState(); ok && len(state.PeerCertificates) > 0 {
		leaf := state.PeerCertificates[0]
		r.tlsNotAfter = leaf.NotAfter
		r.tlsIssuer = leaf.Issuer.CommonName
	}

	r.readRootDSE(conn)
	r.readMonitor(conn)

	suffixes := c.Suffixes
	if len(suffixes) == 0 {
		suffixes = r.namingContexts
	}
	for _, s := range suffixes {
		r.readContextCSN(conn, s)
	}

	for _, e := range c.Entries {
		if len(e.Targets) > 0 && !contains(e.Targets, t.Name) {
			continue
		}
		r.countEntries(conn, e)
	}

	// cn=config needs its own bind. Do it last, on a separate connection, so a
	// failure here cannot disturb anything above.
	//
	// It is also cached: see configCache. Only a successful read is cached, so
	// a failure is retried on the next scrape rather than hidden for the whole
	// interval.
	if t.ConfigBindDN != "" {
		if e, ok := cache.get(t.Name, c.ResolvedConfigInterval); ok {
			r.cfg = e.sc
			r.searchSecs["config"] = e.secs
			r.configAge = time.Since(e.at)
		} else {
			r.readConfig(t, timeout)
			if r.cfg != nil && r.cfg.readable {
				cache.put(t.Name, r.cfg, r.searchSecs["config"])
			}
		}
	}
	return r
}

// ---------------------------------------------------------------- cn=config cache

// configCache holds the last successful cn=config read per target.
//
// cn=config changes when an operator changes it, not on its own, so scraping it
// every 15s buys no signal. It does cost something: the olcSyncRepl values it
// returns contain a cleartext bind password (see the SECURITY note on consumer),
// and with no LDAPS that crosses the network in the clear on every read. Caching
// it cuts that exposure in proportion to the interval.
type configCache struct {
	mu sync.Mutex
	m  map[string]cachedConfig
}

type cachedConfig struct {
	sc   *serverConfig
	secs float64
	at   time.Time
}

func newConfigCache() *configCache { return &configCache{m: map[string]cachedConfig{}} }

// get returns the cached entry for name if it is younger than ttl. A ttl of
// zero or less disables the cache entirely.
func (c *configCache) get(name string, ttl time.Duration) (cachedConfig, bool) {
	if ttl <= 0 {
		return cachedConfig{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[name]
	if !ok || time.Since(e.at) >= ttl {
		return cachedConfig{}, false
	}
	return e, true
}

func (c *configCache) put(name string, sc *serverConfig, secs float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[name] = cachedConfig{sc: sc, secs: secs, at: time.Now()}
}

func dial(t config.Target, timeout time.Duration, r *result) (*ldap.Conn, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: t.ResolvedTLSSkipVerify, //nolint:gosec // operator's explicit choice
		ServerName:         hostOf(t.URI),
	}
	if t.TLSCAFile != "" {
		pem, err := os.ReadFile(t.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("tls_ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tls_ca_file %s: no certificates found", t.TLSCAFile)
		}
		tlsCfg.RootCAs = pool
	}

	start := time.Now()
	conn, err := ldap.DialURL(t.URI,
		ldap.DialWithDialer(&net.Dialer{Timeout: timeout}),
		ldap.DialWithTLSConfig(tlsCfg))
	if err != nil {
		return nil, err
	}
	if t.ResolvedStartTLS && strings.HasPrefix(t.URI, "ldap://") {
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}
	r.dialSeconds = time.Since(start).Seconds()
	return conn, nil
}

// ---------------------------------------------------------------- root DSE

func (r *result) readRootDSE(conn *ldap.Conn) {
	start := time.Now()
	res, err := conn.Search(ldap.NewSearchRequest("", ldap.ScopeBaseObject, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=*)", []string{"namingContexts", "supportedLDAPVersion", "vendorVersion"}, nil))
	r.searchSecs["rootdse"] = time.Since(start).Seconds()
	if err != nil {
		r.errs["rootdse"] = err
		return
	}
	if len(res.Entries) == 0 {
		return
	}
	e := res.Entries[0]
	r.namingContexts = e.GetAttributeValues("namingContexts")
	r.vendorVersion = e.GetAttributeValue("vendorVersion")
}

// ---------------------------------------------------------------- contextCSN

func (r *result) readContextCSN(conn *ldap.Conn, suffix string) {
	start := time.Now()
	res, err := conn.Search(ldap.NewSearchRequest(suffix, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=*)", []string{"contextCSN"}, nil))
	r.searchSecs["contextcsn"] += time.Since(start).Seconds()
	if err != nil {
		r.errs["contextcsn:"+suffix] = err
		return
	}
	if len(res.Entries) == 0 {
		r.errs["contextcsn:"+suffix] = fmt.Errorf("suffix %s: no entry", suffix)
		return
	}
	set, perrs := csn.ParseSet(res.Entries[0].GetAttributeValues("contextCSN"))
	if len(perrs) > 0 {
		r.errs["contextcsn:"+suffix] = perrs[0]
	}
	r.csn[suffix] = set
}

// ---------------------------------------------------------------- entry counts

func (r *result) countEntries(conn *ldap.Conn, e config.EntryCount) {
	scope := ldap.ScopeWholeSubtree
	switch e.Scope {
	case "base":
		scope = ldap.ScopeBaseObject
	case "one":
		scope = ldap.ScopeSingleLevel
	}
	start := time.Now()
	// "1.1" asks for no attributes: we only need the count.
	req := ldap.NewSearchRequest(e.Base, scope, ldap.NeverDerefAliases, 0, 0, false, e.Filter, []string{"1.1"}, nil)
	res, err := conn.SearchWithPaging(req, 1000)
	r.searchSecs["entries:"+e.Name] = time.Since(start).Seconds()
	if err != nil {
		// A sizelimit hit still returns what it got; report that and the error.
		if res != nil {
			r.entries[e.Name] = float64(len(res.Entries))
		}
		r.errs["entries:"+e.Name] = err
		return
	}
	r.entries[e.Name] = float64(len(res.Entries))
}

// ---------------------------------------------------------------- cn=config

func (r *result) readConfig(t config.Target, timeout time.Duration) {
	tmp := &result{}
	conn, err := dial(t, timeout, tmp)
	if err != nil {
		r.errs["config"] = fmt.Errorf("dial: %w", err)
		return
	}
	defer conn.Close()
	conn.SetTimeout(timeout)
	if err := conn.Bind(t.ConfigBindDN, t.ResolvedConfigPassword); err != nil {
		r.errs["config"] = fmt.Errorf("bind as %s: %w", t.ConfigBindDN, err)
		return
	}

	sc := &serverConfig{}
	start := time.Now()
	defer func() { r.searchSecs["config"] = time.Since(start).Seconds() }()

	// cn=config itself
	res, err := conn.Search(ldap.NewSearchRequest("cn=config", ldap.ScopeBaseObject, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=*)", []string{"olcServerID", "olcLogLevel"}, nil))
	if err != nil {
		r.errs["config"] = err
		return
	}
	if len(res.Entries) > 0 {
		e := res.Entries[0]
		sc.serverIDs = e.GetAttributeValues("olcServerID")
		sc.serverID = pickServerID(sc.serverIDs, t.URI)
		sc.logLevel = strings.Join(e.GetAttributeValues("olcLogLevel"), " ")
	}

	// databases
	res, err = conn.Search(ldap.NewSearchRequest("cn=config", ldap.ScopeSingleLevel, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=olcDatabaseConfig)",
		[]string{"olcDatabase", "olcSuffix", "olcSyncrepl", "olcMultiProvider", "olcMirrorMode", "olcReadOnly", "olcDbMaxSize"}, nil))
	if err != nil {
		r.errs["config"] = err
		return
	}
	for _, e := range res.Entries {
		// olcDatabase is "{N}backend"; N may be -1 for the frontend, so strip
		// up to the closing brace rather than a digit set.
		backend := e.GetAttributeValue("olcDatabase")
		if i := strings.Index(backend, "}"); i >= 0 {
			backend = backend[i+1:]
		}
		if backend == "config" || backend == "monitor" || backend == "frontend" {
			continue
		}
		db := dbConfig{
			dn:            e.DN,
			suffix:        e.GetAttributeValue("olcSuffix"),
			backend:       backend,
			multiProvider: isTrue(e.GetAttributeValue("olcMultiProvider")),
			mirrorMode:    isTrue(e.GetAttributeValue("olcMirrorMode")),
			readOnly:      isTrue(e.GetAttributeValue("olcReadOnly")),
		}
		if v := e.GetAttributeValue("olcDbMaxSize"); v != "" {
			db.maxSize, _ = strconv.ParseFloat(v, 64)
		}
		// s is the raw olcSyncRepl value and contains a cleartext bind password
		// (credentials=...). It is handed straight to parseSyncrepl and must
		// never be logged, wrapped into an error, or stored anywhere else.
		for _, s := range e.GetAttributeValues("olcSyncrepl") {
			db.consumers = append(db.consumers, parseSyncrepl(s))
		}
		sc.databases = append(sc.databases, db)
	}

	// syncprov overlays
	res, err = conn.Search(ldap.NewSearchRequest("cn=config", ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=olcSyncProvConfig)", []string{"olcSpCheckpoint", "olcSpSessionlog"}, nil))
	if err == nil {
		for _, e := range res.Entries {
			sp := syncprovConfig{
				dbDN:       parentDN(e.DN),
				checkpoint: e.GetAttributeValue("olcSpCheckpoint"),
			}
			if v := e.GetAttributeValue("olcSpSessionlog"); v != "" {
				sp.sessionlog, _ = strconv.ParseFloat(v, 64)
			}
			sc.syncprov = append(sc.syncprov, sp)
		}
	}
	sc.readable = true
	r.cfg = sc
}

// pickServerID handles both "31" and the multi-valued "31 ldap://host" form,
// choosing the entry whose URL names this target when there is more than one.
func pickServerID(values []string, uri string) int {
	me := strings.ToLower(hostOf(uri))
	first := -1
	for _, v := range values {
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		id, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		if first < 0 {
			first = id
		}
		if len(f) > 1 && strings.EqualFold(hostOf(f[1]), me) {
			return id
		}
	}
	return first
}

// syncreplToken matches key=value where value is either "quoted" or bare.
var syncreplToken = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|(\S+))`)

func parseSyncrepl(s string) consumer {
	c := consumer{}
	kv := map[string]string{}
	for _, m := range syncreplToken.FindAllStringSubmatch(s, -1) {
		v := m[2]
		if v == "" {
			v = m[3]
		}
		kv[strings.ToLower(m[1])] = v
	}
	c.rid = kv["rid"]
	c.provider = kv["provider"]
	c.providerHost = strings.ToLower(hostOf(kv["provider"]))
	c.searchbase = kv["searchbase"]
	c.typ = kv["type"]
	c.retry = kv["retry"]
	c.keepalive = kv["keepalive"]
	c.interval = kv["interval"]
	c.filter = kv["filter"]
	c.bindmethod = kv["bindmethod"]
	// retry="5 5 300 +" — the final field being "+" means retry forever
	f := strings.Fields(c.retry)
	c.retryForever = len(f) > 0 && f[len(f)-1] == "+"
	return c
}

// ---------------------------------------------------------------- helpers

func hostOf(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		// tolerate bare host[:port]
		h := uri
		if i := strings.Index(h, "://"); i >= 0 {
			h = h[i+3:]
		}
		if h2, _, err := net.SplitHostPort(h); err == nil {
			return h2
		}
		return strings.TrimSuffix(h, "/")
	}
	return u.Hostname()
}

func isTrue(s string) bool { return strings.EqualFold(s, "TRUE") }

func parentDN(dn string) string {
	if i := strings.Index(dn, ","); i >= 0 {
		return dn[i+1:]
	}
	return ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
