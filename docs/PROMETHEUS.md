# Prometheus integration

## The short version

The exporter already fans out to every LDAP node itself, so Prometheus only ever scrapes **one HTTP
endpoint per exporter process** — not one per LDAP node.

```yaml
# prometheus.yml
scrape_configs:
  - job_name: openldap
    scrape_interval: 30s
    scrape_timeout: 15s          # must exceed the exporter's own scrape_timeout (config: scrape_timeout)
    static_configs:
      - targets: ["openldap-exporter.internal:9330"]

rule_files:
  - /etc/prometheus/rules/openldap.rules.yml   # from alerts/openldap.rules.yml in this repo
```

That's the whole integration for a single estate. Everything below is for the cases that need more.

## Why `scrape_timeout` (Prometheus) must exceed `scrape_timeout` (exporter config)

The exporter's own `scrape_timeout` (in `openldap-exporter.yml`) bounds how long it waits on
**each LDAP target** during one `/metrics` request — targets are scraped concurrently, so the whole
request takes roughly as long as the slowest target, not the sum. Prometheus's `scrape_timeout` bounds
how long it waits on **the exporter's HTTP response**. If Prometheus's timeout is shorter, a single
slow LDAP node causes Prometheus to log `context deadline exceeded` and drop the *entire* scrape —
every target's metrics for that interval, not just the slow one.

Rule of thumb: `prometheus scrape_timeout ≥ exporter scrape_timeout + 3-5s` of margin for HTTP/TLS
overhead.

## Static config vs `/probe`

Two ways to point Prometheus at this exporter. Pick one — don't mix them for the same targets.

### A. Static config (recommended default)

Prometheus scrapes `/metrics` once; the exporter has already scraped every configured LDAP node by
the time it responds. Simplest, and it's what makes the cross-node replication metrics possible —
Prometheus never needs to know your LDAP topology at all.

```yaml
scrape_configs:
  - job_name: openldap
    static_configs:
      - targets: ["openldap-exporter.internal:9330"]
```

### B. `/probe?target=<name>` (blackbox-exporter style)

Useful if you want Prometheus's own retry/backoff behavior per LDAP node, or if you're already running
a `file_sd`-driven fleet of probe targets and want this to look the same. Only names present in the
exporter's config are accepted (`400` otherwise) — this exporter holds bind credentials, so it will
never scrape an arbitrary host handed to it in a URL.

```yaml
scrape_configs:
  - job_name: openldap-probe
    metrics_path: /probe
    static_configs:
      - targets:
          - ldap-prod-1
          - ldap-prod-2
          - ldap-prod-3
          - ldap-qa-1
          - ldap-qa-2
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: openldap-exporter.internal:9330
```

Trade-off: in mode B, a failure to reach one LDAP node produces one failed *Prometheus scrape*
(`up{job="openldap-probe"} == 0`) rather than the exporter's own `openldap_up == 0` gauge from a
successful scrape of `/metrics`. Prefer mode A unless you specifically want that behavior — mode A's
`openldap_up` is the more informative signal, since the HTTP scrape to the exporter can succeed even
when a specific LDAP node is down.

## Multiple exporter instances (multiple clusters)

If you run one exporter per cluster rather than one exporter covering everything (see the main
[README](../README.md#one-exporter-per-cluster----yes-and-its-a-normal-choice) for when that's the
right call), add each as its own scrape target and attach a label that identifies which:

```yaml
scrape_configs:
  - job_name: openldap
    static_configs:
      - targets: ["openldap-exporter-main.internal:9330"]
        labels: { cluster: main }
      - targets: ["openldap-exporter-alt.internal:9330"]
        labels: { cluster: alt }
```

`labels:` under `static_configs` is attached by Prometheus to **every metric** scraped from that
target — you don't need the exporter to know about it. This gives you a `cluster` label on every
`openldap_*` series without touching the exporter's own config.

**The dashboard already has a `$cluster` variable wired into every panel** (`dashboards/openldap.json`
— `label_values(openldap_up, cluster)`, `includeAll`, and every panel expression filters on
`cluster=~"$cluster"` alongside `group=~"$group"`). If you don't add the `cluster` label, the variable
is harmless: no series carries it, the default `.*` regex still matches everything, and the dashboard
behaves exactly as it did with one cluster. Add the label and a second dropdown appears for free —
no dashboard edits needed.

You do still want distinct `group:` names per cluster (`main-prod`/`alt-prod` rather than `prod` in
both), even with `$cluster` in place. `group:` is what scopes replication-lag comparison *inside* the
exporter — reusing the same name across two exporters doesn't corrupt anything (every node still
carries its own unique `target` label), but it does mean `$group=prod` alone, without also narrowing
`$cluster`, would show nodes from both meshes on one panel. Two independent, unambiguous filters —
`cluster` for which exporter, `group` for which mesh inside it — is the cleanest way to read the
dashboard once you're past one estate.

### Recommended layering for two clusters

```yaml
# exporter A's config (openldap-exporter-main.yml)
targets:
  - { name: ldap-prod-1, uri: ..., group: main-prod, role: provider }
  - { name: ldap-qa-1,   uri: ..., group: main-prod, role: qa }

# exporter B's config (openldap-exporter-alt.yml) - the smaller cluster
targets:
  - { name: alt-prod-1, uri: ..., group: alt-prod, role: provider }
  - { name: alt-qa-1,   uri: ..., group: alt-prod, role: qa }
```

```yaml
# prometheus.yml
  - job_name: openldap
    static_configs:
      - targets: ["exporter-main:9330"]
        labels: { cluster: main }
      - targets: ["exporter-alt:9330"]
        labels: { cluster: alt }
```

One Grafana dashboard, one Prometheus datasource, `$group` dropdown shows `main-prod`, `alt-prod` (and
any other groups you add) distinctly — no dashboard changes needed. The `cluster` label rides along on
every metric even without a dashboard variable for it, so it's usable in ad-hoc queries or a future
panel regardless.

## Recording rules (optional, for large estates)

If you're running many groups/clusters and the dashboard's `replication_group_max_lag_seconds` panel
gets slow, precompute it:

```yaml
groups:
  - name: openldap.recording
    interval: 30s
    rules:
      - record: openldap:replication_group_in_sync:min
        expr: min by (group, suffix) (openldap_replication_group_in_sync)
```

Not necessary at the scale of a handful of clusters — the exporter itself already does the expensive
cross-node comparison at scrape time, so Prometheus's own query load stays cheap (the group-level
gauges are already pre-aggregated per scrape, not computed by a PromQL join at query time).

## Federation / long-term storage

No special handling needed. `openldap_*` metrics are ordinary gauges/counters — they federate,
remote_write, and work with Thanos/Mimir/Cortex the same as any other exporter's output. The one thing
to preserve across any federation/relabeling layer is the `group`, `target`, and (if you added it)
`cluster` labels — they're what every dashboard panel and alert rule filters on.

## Verifying the integration

```bash
# the exporter's own view of its config, no LDAP connection needed
openldap-exporter -config openldap-exporter.yml -check

# is Prometheus actually scraping it successfully
curl -s http://prometheus:9090/api/v1/targets | \
  python3 -c 'import json,sys; d=json.load(sys.stdin)
for t in d["data"]["activeTargets"]:
    if "openldap" in t["labels"].get("job",""):
        print(t["labels"]["job"], t["health"], t["lastError"])'

# do the replication metrics look sane
curl -s http://prometheus:9090/api/v1/query \
  --data-urlencode 'query=openldap_replication_group_in_sync' | python3 -m json.tool
```
