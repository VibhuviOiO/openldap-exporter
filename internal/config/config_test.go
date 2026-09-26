package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.yml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMinimal(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":9330" || c.MetricsPath != "/metrics" {
		t.Errorf("defaults: %+v", c)
	}
	if c.Targets[0].Group != "default" {
		t.Errorf("group default = %q", c.Targets[0].Group)
	}
}

func TestLoadRejectsNoTargets(t *testing.T) {
	p := write(t, `listen: ":9330"`)
	if _, err := Load(p); err == nil {
		t.Error("expected error for no targets")
	}
}

func TestLoadRejectsBadURI(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: "not-a-uri"
`)
	if _, err := Load(p); err == nil {
		t.Error("expected error for bad uri scheme")
	}
}

func TestLoadRejectsDuplicateNames(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
  - name: a
    uri: ldap://b:389
`)
	if _, err := Load(p); err == nil {
		t.Error("expected error for duplicate target name")
	}
}

func TestDefaultsInherited(t *testing.T) {
	p := write(t, `
defaults:
  bind_dn: cn=Manager,dc=example,dc=com
  password: secret
targets:
  - name: a
    uri: ldap://a:389
  - name: b
    uri: ldap://b:389
    bind_dn: cn=Other,dc=example,dc=com
    password: other-secret
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	a := c.TargetByName("a")
	if a.BindDN != "cn=Manager,dc=example,dc=com" || a.ResolvedPassword != "secret" {
		t.Errorf("target a did not inherit defaults: %+v", a)
	}
	b := c.TargetByName("b")
	if b.BindDN != "cn=Other,dc=example,dc=com" || b.ResolvedPassword != "other-secret" {
		t.Errorf("target b override failed: %+v", b)
	}
}

func TestPasswordFile(t *testing.T) {
	dir := t.TempDir()
	pwFile := filepath.Join(dir, "pw")
	if err := os.WriteFile(pwFile, []byte("filesecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
    bind_dn: cn=Manager,dc=example,dc=com
    password_file: `+pwFile+`
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.TargetByName("a").ResolvedPassword; got != "filesecret" {
		t.Errorf("password_file = %q, want %q (trailing newline must be stripped)", got, "filesecret")
	}
}

func TestBindDNWithoutPasswordRejected(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
    bind_dn: cn=Manager,dc=example,dc=com
`)
	if _, err := Load(p); err == nil {
		t.Error("expected error: bind_dn without password")
	}
}

func TestEnvExpansion(t *testing.T) {
	t.Setenv("TEST_LDAP_PW", "envsecret")
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
    bind_dn: cn=Manager,dc=example,dc=com
    password: ${TEST_LDAP_PW}
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.TargetByName("a").ResolvedPassword; got != "envsecret" {
		t.Errorf("env expansion = %q", got)
	}
}

func TestEnvExpansionUnknownLeftVisible(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
    bind_dn: cn=Manager,dc=example,dc=com
    password: ${THIS_VAR_DOES_NOT_EXIST_12345}
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	// A blank password would silently pass validation as "no password" only if
	// bind_dn were also empty; here it must surface as the literal placeholder
	// so an operator notices the typo rather than getting a mystery bind failure.
	if got := c.TargetByName("a").ResolvedPassword; got != "${THIS_VAR_DOES_NOT_EXIST_12345}" {
		t.Errorf("unknown env var = %q, want literal placeholder preserved", got)
	}
}

func TestEntryCountDefaultsAndValidation(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
entries:
  - name: all
    base: dc=example,dc=com
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	e := c.Entries[0]
	if e.Filter != "(objectClass=*)" || e.Scope != "sub" {
		t.Errorf("entry defaults: %+v", e)
	}
}

func TestEntryCountBadScope(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
entries:
  - name: all
    base: dc=example,dc=com
    scope: bogus
`)
	if _, err := Load(p); err == nil {
		t.Error("expected error for bad scope")
	}
}

func TestEntryCountUnknownTarget(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
entries:
  - name: all
    base: dc=example,dc=com
    targets: ["nope"]
`)
	if _, err := Load(p); err == nil {
		t.Error("expected error for unknown target reference")
	}
}

func TestGroups(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
    group: prod
  - name: b
    uri: ldap://b:389
    group: prod
  - name: c
    uri: ldap://c:389
    group: qa
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Groups()
	if len(got) != 2 || got[0] != "prod" || got[1] != "qa" {
		t.Errorf("Groups() = %v", got)
	}
}

func TestDefaultInSyncTolerance(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Replication.InSyncTolerance.Seconds() != 5 {
		t.Errorf("default in_sync_tolerance = %v", c.Replication.InSyncTolerance)
	}
}

func TestConfigIntervalDefault(t *testing.T) {
	p := write(t, `
targets:
  - name: a
    uri: ldap://a:389
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ResolvedConfigInterval != 10*time.Minute {
		t.Errorf("default config_interval = %v, want 10m", c.ResolvedConfigInterval)
	}
}

func TestConfigIntervalExplicit(t *testing.T) {
	// An explicit 0 must mean "read cn=config every scrape", not "use the
	// default" - that is the only way back to the pre-cache behaviour.
	for _, tc := range []struct {
		yaml string
		want time.Duration
	}{
		{"config_interval: 0s", 0},
		{"config_interval: 30s", 30 * time.Second},
		{"config_interval: 1h", time.Hour},
	} {
		p := write(t, tc.yaml+`
targets:
  - name: a
    uri: ldap://a:389
`)
		c, err := Load(p)
		if err != nil {
			t.Fatalf("%s: %v", tc.yaml, err)
		}
		if c.ResolvedConfigInterval != tc.want {
			t.Errorf("%s: got %v, want %v", tc.yaml, c.ResolvedConfigInterval, tc.want)
		}
	}
}

func TestConfigIntervalRejectsNegative(t *testing.T) {
	p := write(t, `
config_interval: -1m
targets:
  - name: a
    uri: ldap://a:389
`)
	if _, err := Load(p); err == nil {
		t.Error("expected error for negative config_interval")
	}
}
