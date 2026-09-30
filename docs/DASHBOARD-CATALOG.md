# Publishing the dashboard to the Grafana community catalog

Everything needed to publish `dashboards/openldap.json` to
[grafana.com/grafana/dashboards](https://grafana.com/grafana/dashboards/).

Artifacts in this repo:

| File | Use |
|---|---|
| `dashboards/openldap.json` | source of truth, provisioned into the monitoring stack |
| `dashboards/openldap-catalog.json` | **the file you upload** — Classic model with `__inputs` |
| `dashboards/screenshots/openldap-overview.png` | screenshot 1 |
| `dashboards/screenshots/openldap-replication.png` | screenshot 2 |
| `dashboards/screenshots/openldap-full.png` | whole dashboard, for a README embed |

## The one rule that catches people

The catalog accepts the **Classic** model only. The **V2 Resource** export — what
Grafana 13 gives you by default in *Export as code* — is rejected. Grafana's docs
state it directly: *"To publish a dashboard to the Grafana community catalog, you
must export it using the Classic model."*

Regenerate the upload file after any dashboard change:

```bash
python3 dashboards/export-for-catalog.py     # or the inline equivalent in this repo's history
```

It does four things to `openldap.json`:

1. rewrites every datasource reference to `${DS_PROMETHEUS}` (42 of them);
2. removes the `datasource` template variable, which `__inputs` replaces;
3. adds `__inputs` so the import wizard asks which Prometheus to use;
4. adds `__requires` listing Grafana + Prometheus + the panel types used.

The `group`, `target` and `cluster` variables stay — those are the dashboard's
own, not datasource inputs.

## Submit

1. Sign in to a Grafana account at [grafana.com](https://grafana.com) (free).
   Dashboards publish under that account, not under your local Grafana.
2. **My dashboards → Upload dashboard**.
3. Upload `dashboards/openldap-catalog.json`.
4. Fill the metadata:
   - **Title**: `OpenLDAP 2.6 — Replication, Convergence & Health`
   - **Data source**: Prometheus
   - **Description**: use the block below
   - **Screenshots**: upload the two PNGs
   - **README**: paste the markdown below
5. **Save and Publish**. The public page can 404 for several hours before it
   appears. Later edits use **Submit** on the same page, which also updates
   screenshots, logo and README.

## Description (paste into the metadata form)

> OpenLDAP 2.6 monitoring for the vibhuvioio/openldap-exporter. Replication
> topology and convergence via contextCSN, syncrepl agreement links, per-node
> entry counts and replication lag, plus LMDB map usage, cn=Monitor operations,
> connections, threads and TLS expiry.

## README (paste into the listing)

````markdown
# OpenLDAP 2.6 — Replication, Convergence & Health

Works with [vibhuvioio/openldap-exporter](https://github.com/VibhuviOiO/openldap-exporter).

## Required: a `cluster` label

The dashboard groups everything by a `cluster` label that **the exporter does not
emit** — your Prometheus must attach it, one per exporter instance:

```yaml
scrape_configs:
  - job_name: openldap
    static_configs:
      - targets: ['openldap-exporter:9330']
        labels: { cluster: prod }
      - targets: ['openldap-exporter-alt:9331']
        labels: { cluster: staging }
```

Run one exporter instance per directory: the `entries:` searches are global to an
instance, and each directory has its own base DN.

Without this label the `cluster`, `group` and `target` variables come up empty
and most panels show no data.

## Variables

| Variable | Meaning |
|---|---|
| `datasource` | resolved at import |
| `cluster` | one per exporter instance (from the label above) |
| `group` | the exporter's `group:` for a target — nodes that must agree |
| `target` | a single server |

## What the panels answer

Reading a replication problem, in the order that isolates the cause:

1. **Nodes up** — is anything answering.
2. **Links healthy** — did providers observe their consumers connect.
3. **In sync** — do the contextCSNs agree.
4. **Replication lag by sid** — how far behind, per sid.
5. **Consumer links seen by providers** — which node's link is dead while its own
   socket still looks healthy.
6. **Records / Change sets / Last change** per node — did the data actually arrive.

`Last change` is the newest contextCSN timestamp for a node. Identical values
across a cluster mean the nodes converged on the same newest change; a node
lagging behind shows an older timestamp.

## Panels that are empty when healthy

- **Sids missing per target** — only lists sids a node is missing.
- **TLS certificate days remaining** — needs `openldap_tls_certificate_expiry_seconds`,
  which requires TLS enabled on the target.

Empty is the good outcome, not a broken panel.
````

## After publishing

Put the resulting URL in `README.md` and `docs/PROMETHEUS.md`:

```
https://grafana.com/grafana/dashboards/<id>-openldap/
```
