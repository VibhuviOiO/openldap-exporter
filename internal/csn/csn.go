// Package csn parses OpenLDAP Change Sequence Numbers.
//
// A CSN looks like
//
//	20260903181449.153505Z#000000#01f#000000
//	└── timestamp ─────┘ └count┘ └sid┘ └mod┘
//
// The timestamp is UTC to the microsecond. sid is the 3-hex-digit serverID of
// the node that made the change. contextCSN on a suffix entry carries one
// value per sid the database has ever seen; comparing the same sid across
// replicas is how replication lag is measured.
package csn

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CSN is one parsed Change Sequence Number.
type CSN struct {
	Time  time.Time
	Count uint32
	SID   string // 3 lower-case hex digits, e.g. "01f"
	Mod   uint32
	Raw   string
}

var ErrMalformed = errors.New("malformed CSN")

// Parse accepts the canonical 4-part form. The timestamp may carry any number
// of fractional digits, or none at all.
func Parse(s string) (CSN, error) {
	parts := strings.Split(strings.TrimSpace(s), "#")
	if len(parts) != 4 {
		return CSN{}, fmt.Errorf("%w: want 4 '#'-separated fields, got %d in %q", ErrMalformed, len(parts), s)
	}
	t, err := parseTime(parts[0])
	if err != nil {
		return CSN{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	count, err := strconv.ParseUint(parts[1], 16, 32)
	if err != nil {
		return CSN{}, fmt.Errorf("%w: count %q", ErrMalformed, parts[1])
	}
	sid := strings.ToLower(parts[2])
	if len(sid) != 3 {
		return CSN{}, fmt.Errorf("%w: sid %q must be 3 hex digits", ErrMalformed, parts[2])
	}
	if _, err := strconv.ParseUint(sid, 16, 16); err != nil {
		return CSN{}, fmt.Errorf("%w: sid %q not hex", ErrMalformed, parts[2])
	}
	mod, err := strconv.ParseUint(parts[3], 16, 32)
	if err != nil {
		return CSN{}, fmt.Errorf("%w: mod %q", ErrMalformed, parts[3])
	}
	return CSN{Time: t, Count: uint32(count), SID: sid, Mod: uint32(mod), Raw: s}, nil
}

// parseTime handles YYYYmmddHHMMSS[.ffffff]Z.
func parseTime(s string) (time.Time, error) {
	if !strings.HasSuffix(s, "Z") {
		return time.Time{}, fmt.Errorf("timestamp %q lacks Z suffix", s)
	}
	body := strings.TrimSuffix(s, "Z")
	whole, frac, _ := strings.Cut(body, ".")
	if len(whole) != 14 {
		return time.Time{}, fmt.Errorf("timestamp %q: want 14 digits before fraction", s)
	}
	t, err := time.Parse("20060102150405", whole)
	if err != nil {
		return time.Time{}, err
	}
	if frac != "" {
		// pad or truncate to nanoseconds
		for len(frac) < 9 {
			frac += "0"
		}
		ns, err := strconv.Atoi(frac[:9])
		if err != nil {
			return time.Time{}, fmt.Errorf("fraction %q", frac)
		}
		t = t.Add(time.Duration(ns))
	}
	return t.UTC(), nil
}

// ServerIDToSID converts an olcServerID (decimal, 0-4095) to its 3-hex-digit
// sid as it appears in a CSN.
func ServerIDToSID(id int) string { return fmt.Sprintf("%03x", id) }

// SIDToServerID is the inverse of ServerIDToSID.
func SIDToServerID(sid string) (int, error) {
	v, err := strconv.ParseUint(sid, 16, 16)
	return int(v), err
}

// Newer reports whether a is strictly more recent than b. CSNs compare by
// time, then count, then mod — sid is an identifier, not an ordering.
func Newer(a, b CSN) bool {
	if !a.Time.Equal(b.Time) {
		return a.Time.After(b.Time)
	}
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	return a.Mod > b.Mod
}

// Set is the contextCSN of one suffix on one server: at most one CSN per sid.
type Set map[string]CSN

// ParseSet parses every value of a contextCSN attribute. It keeps the newest
// value per sid should a malformed server ever present duplicates.
func ParseSet(values []string) (Set, []error) {
	out := Set{}
	var errs []error
	for _, v := range values {
		c, err := Parse(v)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if prev, ok := out[c.SID]; !ok || Newer(c, prev) {
			out[c.SID] = c
		}
	}
	return out, errs
}

// Lag describes how far one replica is behind the newest known value for a sid.
type Lag struct {
	SID     string
	Behind  time.Duration // 0 when current; negative never
	Missing bool          // sid known to the group, absent on this replica
	Newest  CSN           // the newest value across the group, for reference
}

// Compare measures every sid present anywhere in group against this replica's
// set. A sid missing here is reported with Missing=true and Behind equal to the
// age of the newest value, so a single alert threshold covers both cases.
func Compare(mine Set, group []Set, now time.Time) []Lag {
	newest := map[string]CSN{}
	for _, s := range group {
		for sid, c := range s {
			if prev, ok := newest[sid]; !ok || Newer(c, prev) {
				newest[sid] = c
			}
		}
	}
	for sid, c := range mine {
		if prev, ok := newest[sid]; !ok || Newer(c, prev) {
			newest[sid] = c
		}
	}
	var out []Lag
	for sid, best := range newest {
		l := Lag{SID: sid, Newest: best}
		if have, ok := mine[sid]; ok {
			if Newer(best, have) {
				l.Behind = best.Time.Sub(have.Time)
			}
		} else {
			l.Missing = true
			l.Behind = now.Sub(best.Time)
			if l.Behind < 0 {
				l.Behind = 0
			}
		}
		out = append(out, l)
	}
	return out
}
