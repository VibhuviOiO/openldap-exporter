package collector

import (
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// monitor is the parsed cn=Monitor subtree. One search, walked by DN.
type monitor struct {
	version string // "OpenLDAP: slapd 2.6.8 (...)"

	connTotal   float64
	connCurrent float64
	peers       []string // IPs of currently connected clients

	opsInitiated map[string]float64 // bind, search, ...
	opsCompleted map[string]float64

	bytes, pdu, entries, referrals float64

	threads     map[string]float64 // max, max_pending, open, starting, active, pending, backload
	threadState string

	waitersRead, waitersWrite float64

	startTime   time.Time
	currentTime time.Time

	databases []monDatabase
	overlays  []string
	backends  []string
	listeners []string
}

type monDatabase struct {
	name     string // "Database 2"
	suffixes []string
	backend  string // mdb
	isShadow bool
	readOnly bool
	// back-mdb monitoring, present when olcMonitoring: TRUE
	pagesMax, pagesUsed, pagesFree float64
	readersMax, readersUsed        float64
	entries                        float64
	hasMDB                         bool
}

func (r *result) readMonitor(conn *ldap.Conn) {
	start := time.Now()
	res, err := conn.Search(ldap.NewSearchRequest("cn=Monitor", ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		"(objectClass=*)", []string{"*", "+"}, nil))
	r.searchSecs["monitor"] = time.Since(start).Seconds()
	if err != nil {
		r.errs["monitor"] = err
		return
	}
	r.mon = parseMonitor(res.Entries)
}

func parseMonitor(entries []*ldap.Entry) *monitor {
	m := &monitor{
		opsInitiated: map[string]float64{},
		opsCompleted: map[string]float64{},
		threads:      map[string]float64{},
	}
	dbs := map[string]*monDatabase{}

	for _, e := range entries {
		dn := strings.ToLower(e.DN)
		rdn := firstRDN(e.DN) // value of the leading cn=
		lrdn := strings.ToLower(rdn)

		switch {
		case dn == "cn=monitor":
			m.version = e.GetAttributeValue("monitoredInfo")

		case strings.HasSuffix(dn, ",cn=connections,cn=monitor"):
			switch lrdn {
			case "total":
				m.connTotal = num(e.GetAttributeValue("monitorCounter"))
			case "current":
				m.connCurrent = num(e.GetAttributeValue("monitorCounter"))
			default:
				if addr := e.GetAttributeValue("monitorConnectionPeerAddress"); addr != "" {
					if ip := peerIP(addr); ip != "" {
						m.peers = append(m.peers, ip)
					}
				}
			}

		case strings.HasSuffix(dn, ",cn=operations,cn=monitor"):
			op := strings.ToLower(rdn)
			m.opsInitiated[op] = num(e.GetAttributeValue("monitorOpInitiated"))
			m.opsCompleted[op] = num(e.GetAttributeValue("monitorOpCompleted"))

		case strings.HasSuffix(dn, ",cn=statistics,cn=monitor"):
			v := num(e.GetAttributeValue("monitorCounter"))
			switch lrdn {
			case "bytes":
				m.bytes = v
			case "pdu":
				m.pdu = v
			case "entries":
				m.entries = v
			case "referrals":
				m.referrals = v
			}

		case strings.HasSuffix(dn, ",cn=threads,cn=monitor"):
			info := e.GetAttributeValue("monitoredInfo")
			key := strings.ReplaceAll(lrdn, " ", "_")
			if key == "state" {
				m.threadState = info
			} else if f, ok := numOK(info); ok {
				m.threads[key] = f
			}

		case strings.HasSuffix(dn, ",cn=waiters,cn=monitor"):
			v := num(e.GetAttributeValue("monitorCounter"))
			switch lrdn {
			case "read":
				m.waitersRead = v
			case "write":
				m.waitersWrite = v
			}

		case strings.HasSuffix(dn, ",cn=time,cn=monitor"):
			ts := parseMonitorTime(e.GetAttributeValue("monitorTimestamp"))
			switch lrdn {
			case "start":
				m.startTime = ts
			case "current":
				m.currentTime = ts
			}

		case strings.HasSuffix(dn, ",cn=databases,cn=monitor"):
			if !strings.HasPrefix(lrdn, "database ") {
				continue
			}
			db := dbs[rdn]
			if db == nil {
				db = &monDatabase{name: rdn}
				dbs[rdn] = db
			}
			if v := e.GetAttributeValues("namingContexts"); len(v) > 0 {
				db.suffixes = v
			}
			if v := e.GetAttributeValue("monitoredInfo"); v != "" {
				db.backend = v
			}
			db.isShadow = isTrue(e.GetAttributeValue("monitorIsShadow"))
			db.readOnly = isTrue(e.GetAttributeValue("readOnly"))
			for _, a := range e.Attributes {
				if !strings.HasPrefix(a.Name, "olmMDB") || len(a.Values) == 0 {
					continue
				}
				f, ok := numOK(a.Values[0])
				if !ok {
					continue
				}
				db.hasMDB = true
				switch a.Name {
				case "olmMDBPagesMax":
					db.pagesMax = f
				case "olmMDBPagesUsed":
					db.pagesUsed = f
				case "olmMDBPagesFree":
					db.pagesFree = f
				case "olmMDBReadersMax":
					db.readersMax = f
				case "olmMDBReadersUsed":
					db.readersUsed = f
				case "olmMDBEntries":
					db.entries = f
				}
			}

		case strings.HasSuffix(dn, ",cn=overlays,cn=monitor"):
			if v := e.GetAttributeValue("monitoredInfo"); v != "" {
				m.overlays = append(m.overlays, v)
			}
		case strings.HasSuffix(dn, ",cn=backends,cn=monitor"):
			if v := e.GetAttributeValue("monitoredInfo"); v != "" {
				m.backends = append(m.backends, v)
			}
		case strings.HasSuffix(dn, ",cn=listeners,cn=monitor"):
			if v := e.GetAttributeValue("monitorConnectionLocalAddress"); v != "" {
				m.listeners = append(m.listeners, v)
			}
		}
	}

	// keep database order stable: Database 0, 1, 2 ...
	for _, k := range sortedKeys(dbs) {
		m.databases = append(m.databases, *dbs[k])
	}
	return m
}

// firstRDN returns the value of the leading RDN: "cn=Database 2,cn=..." -> "Database 2".
func firstRDN(dn string) string {
	head := dn
	if i := strings.Index(dn, ","); i >= 0 {
		head = dn[:i]
	}
	if i := strings.Index(head, "="); i >= 0 {
		return head[i+1:]
	}
	return head
}

// peerIP extracts the address from "IP=10.3.5.33:59996" or "PATH=/run/slapd/ldapi".
func peerIP(s string) string {
	if !strings.HasPrefix(s, "IP=") {
		return ""
	}
	hp := strings.TrimPrefix(s, "IP=")
	// IPv6 form is IP=[::1]:389
	if strings.HasPrefix(hp, "[") {
		if i := strings.Index(hp, "]"); i > 0 {
			return hp[1:i]
		}
	}
	if i := strings.LastIndex(hp, ":"); i > 0 {
		return hp[:i]
	}
	return hp
}

// parseMonitorTime handles the generalized-time forms cn=Monitor emits.
func parseMonitorTime(s string) time.Time {
	for _, layout := range []string{"20060102150405Z", "20060102150405.000000Z", "20060102150405.000Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func num(s string) float64 {
	f, _ := numOK(s)
	return f
}

func numOK(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil
}

func sortedKeys(m map[string]*monDatabase) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// simple insertion sort; the list is tiny
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
