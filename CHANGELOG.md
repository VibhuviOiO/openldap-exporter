# Changelog

## Unreleased

Initial release.

- `config_interval` (default 10m): `cn=config` is cached per target and re-read on its own interval
  rather than on every scrape. Without LDAPS the `olcSyncRepl` values it returns put a cleartext bind
  password on the wire, and `cn=config` is near-static, so scraping it at the metrics interval costs
  exposure for no signal. `config_interval: 0` restores per-scrape reads. New metric
  `openldap_config_scrape_age_seconds` reports the age of the cached data; only successful reads are
  cached, so a failure is retried on the next scrape.
- Multi-target concurrent scraping of root DSE, `cn=Monitor`, `contextCSN`, `cn=config`, and
  arbitrary entry-count searches.
- Cross-node replication lag and missing-sid detection, grouped by `group:`.
- Provider-side syncrepl link presence (`openldap_syncrepl_link_seen_on_provider`) via
  `cn=Connections` — detects a blackholed/half-open consumer link that looks healthy from the
  consumer's own socket state.
- Config-correctness metrics: serverID, `olcMultiProvider`/`olcReadOnly`, syncprov overlay count and
  sessionlog, per-consumer keepalive/retry/self-reference, and provider-group labelling for one-way
  topology enforcement.
- back-mdb capacity metrics (map size, pages used/free, reader slots) when `olcMonitoring: TRUE`.
- `/probe?target=<name>` for per-target scrape jobs; only configured target names are accepted.
- 24 alerting rules across availability, replication, config-drift, and capacity.
- A Grafana dashboard (Overview / Topology / Replication / cn=Monitor / Capacity) with
  `group`/`target`/`cluster` variables, a restart annotation, and a live Node Graph topology panel
  driven by the same provider-side link-presence metric as the alerting.
- `link_id` label on `openldap_syncrepl_link_seen_on_provider` (`consumer-rid`, collision-free by
  construction) — a stable edge identity for topology graphs.
- Security: `TestParseSyncreplNeverExposesCredentials` enforces by reflection that no field parsed
  from an `olcSyncRepl` statement (which carries a cleartext bind password) can reach a metric label,
  log line, or dashboard.
- Verified read-only: no `Add`/`Modify`/`Delete`/`ModifyDN` LDAP operation exists anywhere in the
  codebase.
