// Package collector implements prometheus.Collector for one or more OpenLDAP
// servers. Every scrape fans out to all targets concurrently, then computes
// the cross-node metrics - replication lag, missing sids, provider-side link
// presence - that a single-node exporter cannot produce.
package collector

import (
	"context"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/VibhuviOiO/openldap-exporter/internal/config"
	"github.com/VibhuviOiO/openldap-exporter/internal/csn"
)

// Build metadata, set with -ldflags "-X ...".
var (
	Version  = "dev"
	Revision = "unknown"
)

// Collector scrapes every configured target on each Collect.
type Collector struct {
	cfg *config.Config
	log *slog.Logger

	// only lets one Collect run at a time; a slow LDAP server must not let
	// Prometheus pile up concurrent scrapes.
	mu sync.Mutex

	// ccache serves cn=config between config_interval reads.
	ccache *configCache
}

// New returns a Collector for cfg.
func New(cfg *config.Config, log *slog.Logger) *Collector {
	return &Collector{cfg: cfg, log: log, ccache: newConfigCache()}
}

// Describe is intentionally empty: this is an unchecked collector, since the
// metric set depends on what each server exposes.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {}

// Collect runs one scrape of every target and emits the results.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ch <- prometheus.MustNewConstMetric(dBuildInfo, prometheus.GaugeValue, 1, Version, Revision, runtime.Version())

	results := c.scrapeAll()
	now := time.Now()

	for _, r := range results {
		c.emitTarget(ch, r, now)
	}
	c.emitReplication(ch, results, now)
	c.emitLinks(ch, results)
}

func (c *Collector) scrapeAll() []*result {
	out := make([]*result, len(c.cfg.Targets))
	var wg sync.WaitGroup
	for i, t := range c.cfg.Targets {
		wg.Add(1)
		go func(i int, t config.Target) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), c.cfg.ScrapeTimeout)
			defer cancel()
			start := time.Now()
			r := scrape(ctx, t, c.cfg, c.ccache)
			r.searchSecs["_total"] = time.Since(start).Seconds()
			for phase, err := range r.errs {
				c.log.Warn("scrape phase failed", "target", t.Name, "phase", phase, "err", err)
			}
			out[i] = r
		}(i, t)
	}
	wg.Wait()
	return out
}

// ---------------------------------------------------------------- per target

func (c *Collector) emitTarget(ch chan<- prometheus.Metric, r *result, now time.Time) {
	t := r.target
	L := func(extra ...string) []string { return append([]string{t.Name, t.Group, t.Role}, extra...) }
	g := func(d *prometheus.Desc, v float64, extra ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, L(extra...)...)
	}
	cnt := func(d *prometheus.Desc, v float64, extra ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, L(extra...)...)
	}

	g(dUp, b2f(r.up))
	g(dScrapeDuration, r.searchSecs["_total"])
	for phase := range r.errs {
		g(dScrapeError, 1, phase)
	}
	if !r.up {
		return
	}

	g(dDialSeconds, r.dialSeconds)
	if t.BindDN != "" {
		g(dBindSeconds, r.bindSeconds)
	}
	for name, secs := range r.searchSecs {
		if name == "_total" {
			continue
		}
		g(dSearchSeconds, secs, name)
	}

	if !r.tlsNotAfter.IsZero() {
		g(dTLSExpiry, float64(r.tlsNotAfter.Unix()))
		g(dTLSInfo, 1, r.tlsIssuer)
	}

	for name, n := range r.entries {
		base := ""
		for _, e := range c.cfg.Entries {
			if e.Name == name {
				base = e.Base
			}
		}
		g(dEntries, n, name, base)
	}

	// ---- contextCSN
	for suffix, set := range r.csn {
		g(dCSNSIDs, float64(len(set)), suffix)
		for sid, v := range set {
			g(dCSNTime, float64(v.Time.UnixNano())/1e9, suffix, sid)
		}
	}

	// ---- cn=Monitor
	if m := r.mon; m != nil {
		if m.version != "" {
			g(dServerInfo, 1, m.version)
		}
		cnt(dConnTotal, m.connTotal)
		g(dConnCurrent, m.connCurrent)
		for op, v := range m.opsInitiated {
			cnt(dOpsInit, v, op)
		}
		for op, v := range m.opsCompleted {
			cnt(dOpsDone, v, op)
		}
		cnt(dBytes, m.bytes)
		cnt(dPDU, m.pdu)
		cnt(dEntriesSent, m.entries)
		cnt(dReferrals, m.referrals)
		for kind, v := range m.threads {
			g(dThreads, v, kind)
		}
		if m.threadState != "" {
			g(dThreadState, 1, m.threadState)
		}
		g(dWaiters, m.waitersRead, "read")
		g(dWaiters, m.waitersWrite, "write")
		if !m.startTime.IsZero() {
			g(dStartTime, float64(m.startTime.Unix()))
			if !m.currentTime.IsZero() {
				g(dUptime, m.currentTime.Sub(m.startTime).Seconds())
			}
		}
		if !m.currentTime.IsZero() {
			g(dServerTime, float64(m.currentTime.Unix()))
			g(dClockOff, m.currentTime.Sub(now).Seconds())
		}
		for _, db := range m.databases {
			suffix := strings.Join(db.suffixes, ";")
			g(dDBInfo, 1, db.name, suffix, db.backend)
			// shadow status is meaningless for the internal databases
			if db.backend != "config" && db.backend != "monitor" && db.backend != "frontend" {
				g(dDBShadow, b2f(db.isShadow), db.name, suffix)
			}
			if db.hasMDB {
				g(dMDBPagesMax, db.pagesMax, db.name, suffix)
				g(dMDBPagesUsed, db.pagesUsed, db.name, suffix)
				g(dMDBPagesFree, db.pagesFree, db.name, suffix)
				if db.pagesMax > 0 {
					g(dMDBPagesRatio, db.pagesUsed/db.pagesMax, db.name, suffix)
				}
				g(dMDBReadersMax, db.readersMax, db.name, suffix)
				g(dMDBReadersUsed, db.readersUsed, db.name, suffix)
				g(dMDBEntries, db.entries, db.name, suffix)
			}
		}
		for _, ov := range m.overlays {
			g(dOverlayInfo, 1, ov)
		}
	}

	// ---- cn=config
	if t.ConfigBindDN != "" {
		g(dCfgReadable, b2f(r.cfg != nil && r.cfg.readable))
		g(dCfgAge, r.configAge.Seconds())
	}
	if sc := r.cfg; sc != nil && sc.readable {
		if sc.serverID >= 0 {
			g(dServerID, float64(sc.serverID))
		}
		if sc.logLevel != "" {
			g(dLogLevelInfo, 1, sc.logLevel)
		}
		spCount := map[string]int{}
		spLog := map[string]float64{}
		for _, sp := range sc.syncprov {
			spCount[sp.dbDN]++
			spLog[sp.dbDN] = sp.sessionlog
		}
		total := 0
		myHost := strings.ToLower(hostOf(t.URI))
		for _, db := range sc.databases {
			g(dDBMultiProv, b2f(db.multiProvider || db.mirrorMode), db.suffix, db.backend)
			g(dDBReadOnly, b2f(db.readOnly), db.suffix, db.backend)
			if db.maxSize > 0 {
				g(dDBMaxSize, db.maxSize, db.suffix, db.backend)
			}
			g(dSpOverlays, float64(spCount[db.dn]), db.suffix)
			if n := spCount[db.dn]; n > 0 {
				g(dSpSessionlog, spLog[db.dn], db.suffix)
			}
			for _, cs := range db.consumers {
				total++
				g(dConsumerInfo, 1, cs.rid, cs.provider, cs.providerHost, cs.searchbase, cs.typ, cs.bindmethod)
				g(dConsumerKA, b2f(cs.keepalive != ""), cs.rid, cs.providerHost)
				g(dConsumerRetry, b2f(cs.retryForever), cs.rid, cs.providerHost)
				g(dConsumerSelf, b2f(cs.providerHost == myHost || contains(r.addrs, cs.providerHost)), cs.rid, cs.providerHost)
				if pt := c.targetForHost(cs.providerHost); pt != nil {
					g(dConsumerProvGroup, 1, cs.rid, cs.providerHost, pt.Group)
				}
			}
		}
		g(dConsumers, float64(total))
	}
}

// ---------------------------------------------------------------- replication

func (c *Collector) emitReplication(ch chan<- prometheus.Metric, results []*result, now time.Time) {
	tol := c.cfg.Replication.InSyncTolerance

	// group -> suffix -> sets from every up member
	type key struct{ group, suffix string }
	groupSets := map[key][]csn.Set{}
	for _, r := range results {
		if !r.up {
			continue
		}
		for suffix, set := range r.csn {
			k := key{r.target.Group, suffix}
			groupSets[k] = append(groupSets[k], set)
		}
	}

	groupWorst := map[key]time.Duration{}
	groupOK := map[key]bool{}
	for k := range groupSets {
		groupOK[k] = true
	}

	for _, r := range results {
		if !r.up {
			continue
		}
		t := r.target
		for suffix, mine := range r.csn {
			k := key{t.Group, suffix}
			peers := groupSets[k]

			// declared expectations, so a sid that has never written is still visible
			var expected []csn.Set
			if sids, ok := c.cfg.Replication.ExpectedSIDs[t.Group]; ok {
				exp := csn.Set{}
				for _, sid := range sids {
					if _, have := mine[sid]; !have {
						// zero-time placeholder: reported as missing with age = now
						exp[sid] = csn.CSN{SID: sid, Time: now}
					}
				}
				if len(exp) > 0 {
					expected = append(expected, exp)
				}
			}

			inSync := true
			for _, lag := range csn.Compare(mine, append(peers, expected...), now) {
				// A sid declared in expected_sids that no member has ever seen
				// has no real "newest" value (Raw is empty), so a lag figure
				// would be invented. Report it only as missing.
				if !(lag.Missing && lag.Newest.Raw == "") {
					ch <- prometheus.MustNewConstMetric(dReplLag, prometheus.GaugeValue, lag.Behind.Seconds(),
						t.Name, t.Group, t.Role, suffix, lag.SID)
				}
				if lag.Missing {
					ch <- prometheus.MustNewConstMetric(dReplMissing, prometheus.GaugeValue, 1,
						t.Name, t.Group, t.Role, suffix, lag.SID)
				}
				if lag.Behind > tol || lag.Missing {
					inSync = false
				}
				if lag.Behind > groupWorst[k] {
					groupWorst[k] = lag.Behind
				}
			}
			ch <- prometheus.MustNewConstMetric(dReplInSync, prometheus.GaugeValue, b2f(inSync),
				t.Name, t.Group, t.Role, suffix)
			if !inSync {
				groupOK[k] = false
			}
		}
	}

	for k := range groupSets {
		ch <- prometheus.MustNewConstMetric(dGroupMaxLag, prometheus.GaugeValue, groupWorst[k].Seconds(), k.group, k.suffix)
		ch <- prometheus.MustNewConstMetric(dGroupInSync, prometheus.GaugeValue, b2f(groupOK[k]), k.group, k.suffix)
	}
}

// ---------------------------------------------------------------- links

// emitLinks answers, for every consumer statement, "does the provider actually
// hold a connection from this consumer right now?" using the provider's own
// cn=Connections. Only possible when the provider is also a scraped target.
func (c *Collector) emitLinks(ch chan<- prometheus.Metric, results []*result) {
	byName := map[string]*result{}
	for _, r := range results {
		byName[r.target.Name] = r
	}
	for _, r := range results {
		if r.cfg == nil || !r.cfg.readable {
			continue
		}
		for _, db := range r.cfg.databases {
			for _, cs := range db.consumers {
				pt := c.targetForHost(cs.providerHost)
				if pt == nil {
					continue
				}
				pr := byName[pt.Name]
				if pr == nil || pr.mon == nil {
					continue
				}
				seen := false
				for _, ip := range pr.mon.peers {
					if contains(r.addrs, ip) {
						seen = true
						break
					}
				}
				ch <- prometheus.MustNewConstMetric(dLinkSeen, prometheus.GaugeValue, b2f(seen),
					r.target.Name, pt.Name, cs.rid, r.target.Group, r.target.Name+"-"+cs.rid)
			}
		}
	}
}

// targetForHost finds the configured target whose URI names host.
func (c *Collector) targetForHost(host string) *config.Target {
	host = strings.ToLower(host)
	for i := range c.cfg.Targets {
		if strings.EqualFold(hostOf(c.cfg.Targets[i].URI), host) {
			return &c.cfg.Targets[i]
		}
	}
	// second pass: short name vs FQDN
	short := strings.SplitN(host, ".", 2)[0]
	for i := range c.cfg.Targets {
		th := strings.ToLower(hostOf(c.cfg.Targets[i].URI))
		if strings.SplitN(th, ".", 2)[0] == short {
			return &c.cfg.Targets[i]
		}
	}
	return nil
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// ftoa is used by tests.
func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
