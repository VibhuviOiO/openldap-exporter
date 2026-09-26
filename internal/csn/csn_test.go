package csn

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	c, err := Parse("20260903181449.153505Z#000000#01f#000000")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 3, 18, 14, 49, 153505000, time.UTC)
	if !c.Time.Equal(want) {
		t.Errorf("time = %v, want %v", c.Time, want)
	}
	if c.SID != "01f" || c.Count != 0 || c.Mod != 0 {
		t.Errorf("fields = %+v", c)
	}
}

func TestParseNoFraction(t *testing.T) {
	c, err := Parse("20160804010030Z#000000#000#000000")
	if err != nil {
		t.Fatal(err)
	}
	if c.Time.Year() != 2016 || c.SID != "000" {
		t.Errorf("got %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	for _, bad := range []string{
		"",
		"20260903181449Z#000000#01f",        // 3 fields
		"20260903181449#000000#01f#000000",  // no Z
		"20260903181449Z#000000#1f#000000",  // sid too short
		"20260903181449Z#000000#zzz#000000", // sid not hex
		"2026090318144Z#000000#01f#000000",  // 13 digits
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestServerID(t *testing.T) {
	cases := map[int]string{31: "01f", 32: "020", 33: "021", 41: "029", 42: "02a", 0: "000", 4095: "fff"}
	for id, sid := range cases {
		if got := ServerIDToSID(id); got != sid {
			t.Errorf("ServerIDToSID(%d) = %s, want %s", id, got, sid)
		}
		back, err := SIDToServerID(sid)
		if err != nil || back != id {
			t.Errorf("SIDToServerID(%s) = %d,%v, want %d", sid, back, err, id)
		}
	}
}

func TestNewer(t *testing.T) {
	a, _ := Parse("20260903181449.000000Z#000000#01f#000000")
	b, _ := Parse("20260903181450.000000Z#000000#01f#000000")
	c, _ := Parse("20260903181449.000000Z#000001#01f#000000")
	if !Newer(b, a) || Newer(a, b) {
		t.Error("time ordering")
	}
	if !Newer(c, a) {
		t.Error("count ordering")
	}
	if Newer(a, a) {
		t.Error("equal is not newer")
	}
}

func TestCompare(t *testing.T) {
	now := time.Date(2026, 9, 3, 18, 20, 0, 0, time.UTC)
	prod, _ := ParseSet([]string{
		"20260903181449.000000Z#000000#01f#000000",
		"20260831174900.000000Z#000000#015#000000",
	})
	qaBehind, _ := ParseSet([]string{
		"20260903181400.000000Z#000000#01f#000000", // 49s behind
		"20260831174900.000000Z#000000#015#000000",
		"20260903181457.000000Z#000000#029#000000", // QA's own sid, prod lacks it - correct
	})

	lags := Compare(qaBehind, []Set{prod}, now)
	got := map[string]Lag{}
	for _, l := range lags {
		got[l.SID] = l
	}
	if l := got["01f"]; l.Missing || l.Behind != 49*time.Second {
		t.Errorf("01f lag = %+v", l)
	}
	if l := got["015"]; l.Missing || l.Behind != 0 {
		t.Errorf("015 lag = %+v", l)
	}
	if l := got["029"]; l.Missing || l.Behind != 0 {
		t.Errorf("029 lag = %+v (own sid must not be missing)", l)
	}

	// prod compared against QA: 029 is missing on prod, which is what the
	// one-way guarantee looks like from prod's side.
	lags = Compare(prod, []Set{qaBehind}, now)
	got = map[string]Lag{}
	for _, l := range lags {
		got[l.SID] = l
	}
	if l := got["029"]; !l.Missing || l.Behind != now.Sub(qaBehind["029"].Time) {
		t.Errorf("029 on prod = %+v, want Missing with age", l)
	}
}
