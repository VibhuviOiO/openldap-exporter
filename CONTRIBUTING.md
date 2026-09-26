# Contributing

```bash
make fmt vet test build
```

- `internal/csn` is the correctness-critical package (CSN parsing and comparison). Any change there
  needs a test case, not just a build check.
- `internal/collector` talks to real LDAP servers; when you can, verify against a real slapd
  (`slapd -Tt` / a container) rather than only unit tests of the parsers, and say so in the PR.
- Keep `metrics.go` (the `*prometheus.Desc` declarations) as the single source of truth for names,
  help text and labels. [`docs/METRICS.md`](docs/METRICS.md) is hand-maintained from it — update both
  in the same PR.
- New alert rules go in [`alerts/openldap.rules.yml`](alerts/openldap.rules.yml) with a `description`
  that says *what the operator should look at*, not just what tripped.
- `go vet` and `gofmt -l .` must be clean; CI enforces both.

## Hard rule: credentials never leave the process

An `olcSyncRepl` value read from `cn=config` contains a cleartext bind password
(`credentials=...`, sometimes alongside `binddn=...`). `parseSyncrepl` in
`internal/collector/scrape.go` extracts specific named fields into the
`consumer` struct — `credentials` and `binddn` are deliberately not among
them, and every field of that struct can become a Prometheus label, which
means it can end up in a dashboard, a scrape response, or a log line.

`TestParseSyncreplNeverExposesCredentials` enforces this by reflection — it
walks every string field of `consumer` and every metric label value looking
for a known secret, for several key orderings. **Any PR touching
`parseSyncrepl`, `consumer`, or the `openldap_syncrepl_consumer_*` metrics
must keep that test passing, and adding a new field there is not enough by
itself — extend the test's label list too.** The same applies to `bindDN`/
`password`/`config_password` in `internal/config`: never format one into an
error, a log field, or a metric label. Same for the raw `olcSyncRepl` string
itself (`s` in `readConfig`) — it must never be logged, wrapped into an
`error`, or stored outside `parseSyncrepl`'s call.

## Reporting a bug

Include the OpenLDAP version (`slapd -VV`), backend (`mdb`/`hdb`/`bdb`), and whether `cn=config` access
was configured — several metrics only exist with it, and "the metric is missing" is usually that.

## Design principle

This project exists to catch replication failures that look healthy from a single node. If a change
makes a real fault *quieter* (an error swallowed, a `0` exported for "couldn't check" instead of a
`scrape_error`), it will be rejected regardless of how it affects test coverage.
