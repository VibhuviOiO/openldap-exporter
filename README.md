# openldap-exporter

A Prometheus exporter for OpenLDAP that treats **replication as a first-class metric**, not an
afterthought.

Every other OpenLDAP exporter reads `cn=Monitor` on one server and stops there. That answers "is
slapd up" and "how many connections". It cannot answer the two questions that actually take an LDAP
estate down:

- **Are my replicas actually in sync**, or does `cn=Monitor` just look healthy while the
  underlying TCP link is dead?
- **Is my one-way replication guarantee (prod → QA, never QA → prod) actually holding**, or has
  something started writing back?

This exporter scrapes every configured server, parses `contextCSN` on each one, and compares them
**across nodes** to compute real replication lag, missing sids, and provider-side link presence — the
kind of check a human would otherwise run by hand with `ldapsearch` and a stopwatch.

## Why this exists

Built to catch a real, documented OpenLDAP failure mode that config review alone does not: a
`refreshAndPersist` syncrepl connection can sit idle, get silently dropped by a stateful firewall or
NAT with no RST sent to either side, and both the consumer's and the provider's own view of the
connection keep reporting healthy — `ss -tnp` shows `ESTAB` on the consumer, the provider's socket
looks fine too, `olcSyncRepl` is configured correctly, entry counts even still match from the last
successful refresh. Nothing errors. Nothing logs. Replication has simply stopped, invisibly, and it
can stay that way for days or weeks before anyone notices the two sides have diverged.

The only way to catch it is to read `contextCSN` on **every** node and compare them against each
other, and read `cn=Connections` on the **provider** to see whether it actually holds the connection
the consumer's config says it should have. A single-node exporter reading its own `cn=Monitor` cannot
do either — it has nothing to compare against. That cross-node comparison is what most of this
exporter's metrics exist to make continuous, so this failure pages someone in minutes instead of
being found by accident much later.

## What it collects

| Area | Metrics | Source |
|---|---|---|
| **Replication** | per-sid lag, missing sids, in-sync flag, group-wide worst lag, provider-side link presence | `contextCSN` compared across nodes + `cn=Connections` |
| **Config correctness** | serverID, duplicate serverID detection, `olcMultiProvider`/`olcReadOnly`, `olcLogLevel`, syncprov overlay count, `olcSpSessionlog`, per-consumer keepalive/retry/self-reference, one-way group enforcement | `cn=config` |
| **Server health** | up/down, bind & dial latency, uptime, clock skew, TLS cert expiry | root DSE, `cn=Monitor`, TLS handshake |
| **cn=Monitor** | connections, operations by type, bytes/PDU/entries/referrals, thread pool, waiters, per-database info | `cn=Monitor` |
| **Storage (back-mdb)** | LMDB pages used/free/max, map-full ratio, reader slots, entry count | `cn=Monitor` (`olcMonitoring: TRUE`) |
| **Entry counts** | arbitrary named searches as gauges | configured LDAP searches |

Full metric reference: [`docs/METRICS.md`](docs/METRICS.md). Prometheus scrape config, `/probe` vs
static targets, and running one dashboard across multiple clusters:
[`docs/PROMETHEUS.md`](docs/PROMETHEUS.md).

## Security

**Read-only, always.** The exporter's only two LDAP operations anywhere in the codebase are `Bind`
and `Search` — no `Add`/`Modify`/`Delete`/`ModifyDN` code path exists, and go-ldap's write request
types aren't even imported:

```bash
grep -rn 'AddRequest\|ModifyRequest\|DelRequest\|ModifyDNRequest\|PasswordModify' internal/ cmd/
# (nothing)
```

It cannot alter your directory no matter how it's configured.

**No credential is ever exposed in a metric, a log line, or the dashboard.** `cn=config` returns
`olcSyncRepl` values that contain a cleartext bind password (`credentials=...`). The parser that
turns that string into Prometheus labels (`parseSyncrepl` in `internal/collector/scrape.go`) only
extracts named, non-secret fields — `credentials` and `binddn` are not among them, and this is
enforced by `TestParseSyncreplNeverExposesCredentials`, which uses reflection to fail the build if a
future change ever copies a secret into a struct field or a metric label. Your own LDAP bind
password and `cn=config` password are held only in memory, passed straight to `conn.Bind`, and never
formatted into an error, a log field, or anything that leaves the process. See
[`CONTRIBUTING.md`](CONTRIBUTING.md#hard-rule-credentials-never-leave-the-process) for the rule this
enforces on every future change.

**`cn=config` is scraped on its own, longer interval.** Keeping a secret out of the metrics does not
keep it off the network: without LDAPS, the `olcSyncRepl` values returned by the `cn=config` search
cross the wire in the clear, password included, every time that search runs. `cn=config` changes when
an operator changes it, so reading it at the metrics scrape interval buys no signal and multiplies
that exposure. It is therefore cached per target and re-read every `config_interval`
(default **10m**), which at a 15s scrape cuts the number of times that password is transmitted by
40x. Set `config_interval: 0` to read it on every scrape instead.

`openldap_config_scrape_age_seconds` reports how old the cached data is, so a stale or failing
`cn=config` read is visible rather than silent. Only successful reads are cached — a failure is
retried on the next scrape.

The real fix is StartTLS, and `starttls: true` is supported. The cache reduces the exposure; it does
not remove it.

## Quick start

```bash
cp openldap-exporter.example.yml openldap-exporter.yml
# edit targets, bind_dn, password

export LDAP_PASSWORD=...
export LDAP_CONFIG_PASSWORD=...     # optional: enables config-correctness metrics

go run ./cmd/openldap-exporter -config openldap-exporter.yml
# or: make build && ./bin/openldap-exporter -config openldap-exporter.yml
```

```bash
curl localhost:9330/metrics | grep openldap_replication_lag_seconds
```

Validate a config without touching a server:

```bash
openldap-exporter -config openldap-exporter.yml -check
```

### Docker

```bash
docker run -p 9330:9330 \
  -v $PWD/openldap-exporter.yml:/etc/openldap-exporter/openldap-exporter.yml:ro \
  -e LDAP_PASSWORD -e LDAP_CONFIG_PASSWORD \
  vibhuvioio/openldap-exporter:latest
```

Also on GHCR: `ghcr.io/vibhuvioio/openldap-exporter:latest`.

### systemd

See [`deploy/openldap-exporter.service`](deploy/openldap-exporter.service).

## Configuration

One exporter, many targets — a single instance scrapes your whole estate concurrently, which is what
makes the cross-node comparison possible. See the fully-commented
[`openldap-exporter.example.yml`](openldap-exporter.example.yml).

```yaml
targets:
  - name: ldap-prod-1
    uri: ldap://ldap-prod-1.example.com:389
    group: prod          # lag is computed within a group, not across all targets
    role: provider
  - name: ldap-qa-1
    uri: ldap://ldap-qa-1.example.com:389
    group: prod           # a QA node that CONSUMES from prod belongs in prod's group -
    role: qa               # it must carry prod's sids, and that's what lag measures
```

**Why a QA-role node still goes in the `prod` group:** grouping determines which nodes' `contextCSN`
values get compared against each other. A QA node reading from prod must converge on prod's sids, so
it belongs in the same group. Its *own* sids will correctly show as `replication_sid_missing` on the
prod nodes — that's the one-way guarantee being enforced, not a fault. Use
`openldap_syncrepl_consumer_provider_group` and the `OpenLDAPOneWayReplicationBroken` alert to catch
the real failure mode: a prod node consuming from a QA-labelled provider.

**`cn=config` access is optional but strongly recommended.** Without it you get server health and
`cn=Monitor` metrics only. With it (`config_bind_dn` / `config_password`), you also get every
config-correctness and topology metric — including the ones that catch the blackholed-connection
failure above, and a `retry` list without a trailing `+`, which stops a consumer reconnecting for
good the moment the list runs out (observed to take as little as ~25 minutes).

### Multi-target scraping (`/probe`)

`/probe?target=<name>` scrapes exactly one configured target, for use with Prometheus's
[blackbox-style multi-target pattern](https://prometheus.io/docs/guides/multi-target-exporter/) if you
prefer per-target scrape jobs. Only names present in the config are accepted — this exporter holds
credentials, so it will not scrape an arbitrary host from a URL parameter.

## Alerting

[`alerts/openldap.rules.yml`](alerts/openldap.rules.yml) ships 24 rules across four groups:
availability, replication, config drift, and capacity. Every rule maps to a failure this exporter (or
its authors) has actually seen, including:

- `OpenLDAPSyncreplLinkNotOnProvider` — the exact blackholed-connection signature described above
- `OpenLDAPSyncreplNoKeepalive` / `OpenLDAPSyncreplRetryNotForever` — the two config properties that
  make that failure mode possible in the first place
- `OpenLDAPOneWayReplicationBroken` — a prod node consuming from a QA-group provider
- `OpenLDAPLogLevelHidesSync` — `olcLogLevel: stats` alone never logs syncrepl activity at all

## Dashboards

[`dashboards/openldap.json`](dashboards/openldap.json) — import directly into Grafana (Dashboards →
Import → Upload). Six rows: Overview, **Topology**, Replication, cn=Monitor, Capacity, with
`$group`/`$target`/`$cluster` template variables and a restart annotation.

**Topology is a live architecture diagram, not a static drawing.** It's a Grafana
[Node Graph](https://grafana.com/docs/grafana/latest/panels-visualizations/visualizations/node-graph/)
panel built from the same metrics as every other panel: every scraped server is a node, and every
edge is one syncrepl consumer→provider link — drawn from the **provider's own** `cn=Connections`
(`openldap_syncrepl_link_seen_on_provider`), the same source the "Links not seen on provider" panel
uses, not from a consumer's config alone. A link the config claims exists but the provider never
actually accepted looks exactly like what it is on this diagram: a gap. Node ring fill tracks
`openldap_up`. The panel's finer color/threshold styling may want one pass in Grafana's panel editor
to taste after import — the node and edge data itself is exactly what the two queries return.

**One dashboard, any number of clusters.** `$cluster` filters on a `cluster` label you attach at
Prometheus scrape time (see [`docs/PROMETHEUS.md`](docs/PROMETHEUS.md#multiple-exporter-instances-multiple-clusters))
— it's already wired into every panel, including Topology, and does nothing if you don't use it, so
running a second exporter for a second estate needs zero dashboard changes.

## Comparison

| | this exporter | typical single-node exporters |
|---|---|---|
| Scrapes multiple servers and compares them | ✅ | ❌ (one target per process) |
| Replication lag from real `contextCSN` comparison | ✅ | ❌ or approximate |
| Detects a blackholed/half-open syncrepl link | ✅ (provider-side `cn=Connections` check) | ❌ |
| One-way replication enforcement (prod→QA, never back) | ✅ | ❌ |
| `cn=config` correctness (keepalive, retry, serverID collisions, multiprovider) | ✅ | ❌ |
| LMDB capacity (map-full risk) | ✅ | partial |
| Config-driven, multi-target, single binary | ✅ | varies |

## Building

```bash
make build      # ./bin/openldap-exporter
make test       # go test -race -cover ./...
make docker     # local image
make release    # linux/darwin × amd64/arm64 static binaries
```

Requires Go 1.27+. No CGO, no external runtime dependencies.

## Design notes

- **One scrape, five phases, independent failure.** Root DSE, `cn=Monitor`, `contextCSN`, entry
  counts, and `cn=config` are each read separately and each record their own error. A `cn=config` bind
  failure does not blank out the health metrics that came from the anonymous/simple bind.
- **`up` and `scrape_error{phase=...}` are separate from the substantive metrics.** A gauge that is
  simply absent means "not configured" or "not applicable" (e.g. `mdb_pages_max` on a `bdb` backend);
  a `scrape_error` means "we tried to read this and failed" — Prometheus and Grafana handle absence and
  explicit failure very differently, and conflating them (e.g. exporting `0` for "couldn't read") lies
  to your dashboards.
- **CSN parsing is exhaustively unit-tested** ([`internal/csn`](internal/csn)) against the real
  4-field format (`timestamp#count#sid#mod`), including the sid-as-serverID round trip and the
  same-timestamp tie-break by count.
- **No admin password ever touches an LDAP filter, log line, or metric label.**

## License

Apache 2.0 — see [`LICENSE`](LICENSE).
