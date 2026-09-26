package collector

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
)

func TestParseSyncrepl(t *testing.T) {
	s := `rid=201 provider=ldap://ldap-prod-1.example.com:389 binddn="cn=Manager,dc=example,dc=com" bindmethod=simple credentials=s3cret searchbase="dc=example,dc=com" scope=sub type=refreshAndPersist retry="5 5 300 +" timeout=1 keepalive=60:3:10`
	c := parseSyncrepl(s)
	if c.rid != "201" {
		t.Errorf("rid = %q", c.rid)
	}
	if c.providerHost != "ldap-prod-1.example.com" {
		t.Errorf("providerHost = %q", c.providerHost)
	}
	if c.searchbase != "dc=example,dc=com" {
		t.Errorf("searchbase = %q", c.searchbase)
	}
	if c.typ != "refreshAndPersist" || c.bindmethod != "simple" {
		t.Errorf("type/bindmethod = %q/%q", c.typ, c.bindmethod)
	}
	if !c.retryForever {
		t.Error("retry ends with + but retryForever=false")
	}
	if c.keepalive != "60:3:10" {
		t.Errorf("keepalive = %q", c.keepalive)
	}
}

func TestParseSyncreplRetryNotForever(t *testing.T) {
	// a retry list with no trailing "+" gives up for good once exhausted -
	// this exact form has caused a real consumer to stop reconnecting after ~25 minutes
	c := parseSyncrepl(`rid=023 provider=ldap://prod-ldap-3 retry="5 5 300 5" type=refreshAndPersist`)
	if c.retryForever {
		t.Error("retry=\"5 5 300 5\" must not be retryForever")
	}
	if c.keepalive != "" {
		t.Error("no keepalive expected")
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"ldap://ldap-prod-1.example.com:389": "ldap-prod-1.example.com",
		"ldaps://x.example.org":              "x.example.org",
		"ldap://10.3.5.33":                   "10.3.5.33",
		"ldap://[::1]:389":                   "::1",
		"ldapi:///":                          "",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickServerID(t *testing.T) {
	if got := pickServerID([]string{"31"}, "ldap://a:389"); got != 31 {
		t.Errorf("single = %d", got)
	}
	multi := []string{"1 ldap://a.example.com", "2 ldap://b.example.com", "3 ldap://c.example.com"}
	if got := pickServerID(multi, "ldap://b.example.com:389"); got != 2 {
		t.Errorf("multi picked %d, want 2", got)
	}
	if got := pickServerID(multi, "ldap://zzz"); got != 1 {
		t.Errorf("unmatched multi should fall back to first, got %d", got)
	}
	if got := pickServerID(nil, "ldap://a"); got != -1 {
		t.Errorf("empty = %d, want -1", got)
	}
}

func TestPeerIP(t *testing.T) {
	cases := map[string]string{
		"IP=10.3.5.33:59996":    "10.3.5.33",
		"IP=[fe80::1]:389":      "fe80::1",
		"PATH=/run/slapd/ldapi": "",
	}
	for in, want := range cases {
		if got := peerIP(in); got != want {
			t.Errorf("peerIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func entry(dn string, attrs map[string][]string) *ldap.Entry {
	e := &ldap.Entry{DN: dn}
	for k, v := range attrs {
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: k, Values: v})
	}
	return e
}

func TestParseMonitor(t *testing.T) {
	m := parseMonitor([]*ldap.Entry{
		entry("cn=Monitor", map[string][]string{"monitoredInfo": {"OpenLDAP: slapd 2.6.8 (Jun 1 2026)"}}),
		entry("cn=Total,cn=Connections,cn=Monitor", map[string][]string{"monitorCounter": {"12345"}}),
		entry("cn=Current,cn=Connections,cn=Monitor", map[string][]string{"monitorCounter": {"5"}}),
		entry("cn=Connection 17,cn=Connections,cn=Monitor", map[string][]string{"monitorConnectionPeerAddress": {"IP=10.3.5.33:59996"}}),
		entry("cn=Connection 18,cn=Connections,cn=Monitor", map[string][]string{"monitorConnectionPeerAddress": {"IP=10.3.5.34:39274"}}),
		entry("cn=Bind,cn=Operations,cn=Monitor", map[string][]string{"monitorOpInitiated": {"100"}, "monitorOpCompleted": {"99"}}),
		entry("cn=Search,cn=Operations,cn=Monitor", map[string][]string{"monitorOpInitiated": {"500"}, "monitorOpCompleted": {"500"}}),
		entry("cn=Bytes,cn=Statistics,cn=Monitor", map[string][]string{"monitorCounter": {"987654"}}),
		entry("cn=Max,cn=Threads,cn=Monitor", map[string][]string{"monitoredInfo": {"16"}}),
		entry("cn=Active,cn=Threads,cn=Monitor", map[string][]string{"monitoredInfo": {"2"}}),
		entry("cn=Max Pending,cn=Threads,cn=Monitor", map[string][]string{"monitoredInfo": {"0"}}),
		entry("cn=State,cn=Threads,cn=Monitor", map[string][]string{"monitoredInfo": {"running"}}),
		entry("cn=Read,cn=Waiters,cn=Monitor", map[string][]string{"monitorCounter": {"3"}}),
		entry("cn=Start,cn=Time,cn=Monitor", map[string][]string{"monitorTimestamp": {"20260901000000Z"}}),
		entry("cn=Current,cn=Time,cn=Monitor", map[string][]string{"monitorTimestamp": {"20260903181642Z"}}),
		entry("cn=Database 2,cn=Databases,cn=Monitor", map[string][]string{
			"namingContexts": {"dc=example,dc=com"}, "monitoredInfo": {"mdb"},
			"olmMDBPagesMax": {"2621440"}, "olmMDBPagesUsed": {"6210"}, "olmMDBPagesFree": {"12"},
			"olmMDBReadersMax": {"126"}, "olmMDBReadersUsed": {"1"}, "olmMDBEntries": {"5571"},
		}),
		entry("cn=Database 0,cn=Databases,cn=Monitor", map[string][]string{"namingContexts": {"cn=config"}, "monitoredInfo": {"config"}}),
		entry("cn=Overlay 0,cn=Overlays,cn=Monitor", map[string][]string{"monitoredInfo": {"syncprov"}}),
	})

	if m.version == "" || m.connTotal != 12345 || m.connCurrent != 5 {
		t.Errorf("basics: %+v", m)
	}
	if len(m.peers) != 2 || m.peers[0] != "10.3.5.33" {
		t.Errorf("peers = %v", m.peers)
	}
	if m.opsInitiated["bind"] != 100 || m.opsCompleted["search"] != 500 {
		t.Errorf("ops = %v / %v", m.opsInitiated, m.opsCompleted)
	}
	if m.threads["max"] != 16 || m.threads["max_pending"] != 0 || m.threadState != "running" {
		t.Errorf("threads = %v state=%q", m.threads, m.threadState)
	}
	if m.waitersRead != 3 {
		t.Errorf("waiters read = %v", m.waitersRead)
	}
	if m.startTime.IsZero() || m.currentTime.Sub(m.startTime).Hours() < 60 {
		t.Errorf("time: start=%v current=%v", m.startTime, m.currentTime)
	}
	if len(m.databases) != 2 || m.databases[0].name != "Database 0" {
		t.Fatalf("databases = %+v", m.databases)
	}
	db := m.databases[1]
	if !db.hasMDB || db.entries != 5571 || db.pagesMax != 2621440 || db.backend != "mdb" {
		t.Errorf("mdb db = %+v", db)
	}
	if len(m.overlays) != 1 || m.overlays[0] != "syncprov" {
		t.Errorf("overlays = %v", m.overlays)
	}
}

// TestParseSyncreplNeverExposesCredentials is a security invariant, not an
// ordinary correctness test: an olcSyncRepl statement carries a cleartext
// bind password in its "credentials=" field, and every field of `consumer`
// ends up as a Prometheus label (openldap_syncrepl_consumer_info and
// related metrics) - i.e. potentially in logs, dashboards, and scrape
// output. This fails loudly with reflection if a future change adds a field
// that copies the secret in, even under a differently-cased key or with
// extra whitespace, rather than relying on a reviewer noticing a one-line
// diff.
func TestParseSyncreplNeverExposesCredentials(t *testing.T) {
	const secret = "s3cret-tok3n-do-not-leak"
	statements := []string{
		// credentials before binddn
		`rid=201 provider=ldap://a.example.com:389 credentials=` + secret + ` binddn="cn=Manager,dc=example,dc=com" bindmethod=simple searchbase="dc=example,dc=com" type=refreshAndPersist retry="5 5 300 +" keepalive=60:3:10`,
		// binddn before credentials, quoted binddn
		`rid=202 binddn="cn=Manager,dc=example,dc=com" credentials=` + secret + ` provider=ldap://b.example.com:389 bindmethod=simple type=refreshAndPersist`,
		// credentials is the only extra field
		`rid=203 provider=ldap://c.example.com:389 credentials=` + secret,
	}

	for _, stmt := range statements {
		c := parseSyncrepl(stmt)

		// 1. Walk every exported and unexported string field by reflection -
		// this catches the secret even if a future field is added under a
		// name that doesn't obviously look credential-related.
		v := reflect.ValueOf(c)
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if f.Kind() != reflect.String {
				continue
			}
			if strings.Contains(f.String(), secret) {
				t.Fatalf("consumer.%s contains the credential value for statement %q: %q",
					v.Type().Field(i).Name, stmt, f.String())
			}
		}

		// 2. Same check against the metric label values this struct actually
		// produces, since that's the real leak surface.
		labels := []string{c.rid, c.provider, c.providerHost, c.searchbase, c.typ, c.bindmethod, c.retry, c.keepalive, c.interval, c.filter}
		for _, l := range labels {
			if strings.Contains(l, secret) {
				t.Fatalf("a metric label contains the credential value for statement %q: %q", stmt, l)
			}
		}
	}
}

func TestConfigCacheServesWithinTTL(t *testing.T) {
	c := newConfigCache()
	sc := &serverConfig{serverID: 31, readable: true}
	c.put("node1", sc, 0.25)

	e, ok := c.get("node1", time.Minute)
	if !ok {
		t.Fatal("expected a cache hit within the ttl")
	}
	if e.sc != sc || e.secs != 0.25 {
		t.Errorf("cached entry = %+v", e)
	}
	if time.Since(e.at) > time.Second {
		t.Errorf("cached at = %v, expected ~now", e.at)
	}
}

func TestConfigCacheExpires(t *testing.T) {
	c := newConfigCache()
	c.put("node1", &serverConfig{readable: true}, 0)
	// A ttl shorter than the age of the entry must miss.
	if _, ok := c.get("node1", time.Nanosecond); ok {
		t.Error("expected a miss once the entry is older than the ttl")
	}
}

func TestConfigCacheDisabledByZeroTTL(t *testing.T) {
	// config_interval: 0 restores the pre-cache behaviour: cn=config is read
	// on every scrape, so the cache must never serve.
	c := newConfigCache()
	c.put("node1", &serverConfig{readable: true}, 0)
	if _, ok := c.get("node1", 0); ok {
		t.Error("ttl 0 must disable the cache")
	}
	if _, ok := c.get("node1", -time.Second); ok {
		t.Error("negative ttl must disable the cache")
	}
}

func TestConfigCacheMissesUnknownTarget(t *testing.T) {
	c := newConfigCache()
	if _, ok := c.get("never-scraped", time.Hour); ok {
		t.Error("expected a miss for a target that was never cached")
	}
}
