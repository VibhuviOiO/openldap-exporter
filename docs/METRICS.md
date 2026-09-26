# Metric reference

Every metric carries `target`, `group`, `role` unless noted otherwise (`tl` in
[`internal/collector/metrics.go`](../internal/collector/metrics.go)). Names are generated from that
file — if this doc and the code disagree, the code is right; please file an issue.

## Scrape health

| Metric | Type | Extra labels | Meaning |
|---|---|---|---|
| `openldap_up` | gauge | | 1 if the bind (or anonymous connect) succeeded this scrape |
| `openldap_scrape_duration_seconds` | gauge | | wall time of the whole scrape |
| `openldap_scrape_error` | gauge | `phase` | 1 for each phase that failed (`dial`, `bind`, `monitor`, `contextcsn:<suffix>`, `config`, `entries:<name>`); absent when a phase succeeds |
| `openldap_dial_duration_seconds` | gauge | | TCP connect + optional StartTLS |
| `openldap_bind_duration_seconds` | gauge | | simple bind round-trip |
| `openldap_search_duration_seconds` | gauge | `search` | round-trip of each search the exporter ran |
| `openldap_exporter_build_info` | gauge | `version, revision, goversion` | always 1 |
| `openldap_server_info` | gauge | `version` | `cn=Monitor` version string |

## Replication (the reason this exporter exists)

| Metric | Type | Extra labels | Meaning |
|---|---|---|---|
| `openldap_context_csn_timestamp_seconds` | gauge | `suffix, sid` | that sid's CSN timestamp, as Unix seconds |
| `openldap_context_csn_sids` | gauge | `suffix` | distinct sids in this node's `contextCSN` |
| `openldap_replication_lag_seconds` | gauge | `suffix, sid` | how far this node is behind the newest value for that sid **within its group**; 0 when current |
| `openldap_replication_sid_missing` | gauge | `suffix, sid` | 1 if another group member (or `expected_sids`) carries this sid and this node does not |
| `openldap_replication_in_sync` | gauge | `suffix` | 1 if every sid is within `in_sync_tolerance` and none is missing |
| `openldap_replication_group_max_lag_seconds` | gauge | (group-level: `group, suffix`) | worst lag across the whole group |
| `openldap_replication_group_in_sync` | gauge | (group-level: `group, suffix`) | 1 if every member of the group is in sync |
| `openldap_syncrepl_link_seen_on_provider` | gauge | (own labels: `consumer, provider, rid, group, link_id`) | 1 if the **provider's own** `cn=Connections` shows a connection from this consumer's resolved address. This is what catches a blackholed link: the consumer's socket can look `ESTABLISHED` while the provider has no matching entry. Requires `cn=config` access on both ends. `link_id` is `consumer-rid`, guaranteed collision-free (rid is unique per consumer, consumer names are unique config-wide) — it exists to give a topology graph a stable edge id without reconstructing one via string matching; see [`dashboards/openldap.json`](../dashboards/openldap.json)'s Topology row. |

**Reading `sid_missing` correctly:** a QA node's own sid missing from a prod node is *expected* — that
is the one-way guarantee. A *prod* sid missing from another *prod* node in the same group is the
actual fault. Filter/group your alerts by `role` and by which group is being evaluated, not by the
metric alone. `OpenLDAPReplicationSidMissing` in the shipped rules only fires for a target that is
itself up, but you should still scope by role.

## Config correctness (`cn=config`, needs `config_bind_dn`)

| Metric | Type | Extra labels | Meaning |
|---|---|---|---|
| `openldap_config_readable` | gauge | | 1 if `cn=config` data is available |
| `openldap_config_scrape_age_seconds` | gauge | | Age of that data. 0 = read during this scrape; otherwise served from the cache, which refreshes every `config_interval` |
| `openldap_server_id` | gauge | | `olcServerID` |
| `openldap_log_level_info` | gauge | `level` | current `olcLogLevel`; if it doesn't contain `sync`, replication activity is never logged |
| `openldap_database_multiprovider` | gauge | `suffix, backend` | 1 if `olcMultiProvider` (2.5+) or `olcMirrorMode` (2.4) is TRUE |
| `openldap_database_readonly` | gauge | `suffix, backend` | 1 if `olcReadOnly` is TRUE — blocks all client writes, including on a designated consumer |
| `openldap_database_max_size_bytes` | gauge | `suffix, backend` | `olcDbMaxSize` (mdb only) |
| `openldap_syncprov_overlays` | gauge | `suffix` | count of syncprov overlays on this database — must be exactly 1 on a provider |
| `openldap_syncprov_sessionlog` | gauge | `suffix` | `olcSpSessionlog` — how many changes a provider can replay before falling back to a full present-phase resync |
| `openldap_syncrepl_consumers` | gauge | | number of `olcSyncRepl` statements on this node |
| `openldap_syncrepl_consumer_info` | gauge | `rid, provider, provider_host, suffix, type, bindmethod` | one row per statement, always 1 |
| `openldap_syncrepl_consumer_keepalive` | gauge | `rid, provider_host` | 1 if the statement sets `keepalive=`. Without it, an idle `refreshAndPersist` link crossing a stateful firewall can be dropped with no RST on either side — the failure this exporter was built to catch. |
| `openldap_syncrepl_consumer_retry_forever` | gauge | `rid, provider_host` | 1 if `retry=` ends in `+`. Without it, retries stop for good once the list is exhausted and the consumer never reconnects on its own. |
| `openldap_syncrepl_consumer_self_reference` | gauge | `rid, provider_host` | 1 if the provider resolves to this same host — a per-node config file applied on the wrong machine |
| `openldap_syncrepl_consumer_provider_group` | gauge | `rid, provider_host, provider_group` | present when the provider is itself a scraped target; compare `group` (this node's) against `provider_group` to enforce a one-way topology |

## Server health / `cn=Monitor`

| Metric | Type | Extra labels |
|---|---|---|
| `openldap_connections_total` | counter | |
| `openldap_connections_current` | gauge | |
| `openldap_operations_initiated_total` | counter | `operation` |
| `openldap_operations_completed_total` | counter | `operation` |
| `openldap_statistics_bytes_total` | counter | |
| `openldap_statistics_pdu_total` | counter | |
| `openldap_statistics_entries_total` | counter | |
| `openldap_statistics_referrals_total` | counter | |
| `openldap_threads` | gauge | `kind` (`max`, `max_pending`, `open`, `starting`, `active`, `pending`, `backload`) |
| `openldap_thread_state_info` | gauge | `state` |
| `openldap_waiters` | gauge | `direction` (`read`, `write`) |
| `openldap_start_time_seconds` | gauge | |
| `openldap_server_time_seconds` | gauge | slapd's own clock, for cross-node skew comparison |
| `openldap_uptime_seconds` | gauge | |
| `openldap_clock_offset_seconds` | gauge | server time minus the exporter's own wall clock; includes network latency |
| `openldap_database_info` | gauge | `database, suffix, backend` |
| `openldap_database_shadow` | gauge | `database, suffix` |
| `openldap_overlay_info` | gauge | `overlay` |

## Storage (back-mdb, needs `olcMonitoring: TRUE`)

| Metric | Type | Extra labels |
|---|---|---|
| `openldap_mdb_pages_max` | gauge | `database, suffix` |
| `openldap_mdb_pages_used` | gauge | `database, suffix` |
| `openldap_mdb_pages_free` | gauge | `database, suffix` |
| `openldap_mdb_pages_used_ratio` | gauge | `database, suffix` — alert before 0.9; LMDB fails hard (`MDB_MAP_FULL`) at 1.0 |
| `openldap_mdb_readers_max` | gauge | `database, suffix` |
| `openldap_mdb_readers_used` | gauge | `database, suffix` |
| `openldap_mdb_entries` | gauge | `database, suffix` |

## Entry counts

| Metric | Type | Extra labels |
|---|---|---|
| `openldap_entries` | gauge | `name, base` — one per configured `entries:` search |

## TLS

| Metric | Type | Extra labels |
|---|---|---|
| `openldap_tls_certificate_expiry_seconds` | gauge | NotAfter, Unix seconds |
| `openldap_tls_certificate_info` | gauge | `issuer` |
