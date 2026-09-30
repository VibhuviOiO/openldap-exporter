# Publishing the dashboard to the Grafana community catalog

Everything needed to publish `dashboards/openldap.json` to
[grafana.com/grafana/dashboards](https://grafana.com/grafana/dashboards/).

Artifacts in this repo:

| File | Use |
|---|---|
| `dashboards/openldap.json` | source of truth, provisioned into the monitoring stack |
| `dashboards/openldap-catalog.json` | **the file you upload** — Classic model with `__inputs` |
| `dashboards/export-for-catalog.py` | regenerates the upload file from the source dashboard |
| `dashboards/catalog-README.md` | **paste into the README field** of the listing |
| `dashboards/screenshots/openldap-overview.png` | screenshot 1 |
| `dashboards/screenshots/openldap-replication.png` | screenshot 2 |
| `dashboards/screenshots/openldap-full.png` | whole dashboard, for a README embed |

The **logo** is the vibhuvioio brand mark, not an OpenLDAP one — the catalog logo
identifies whoever maintains the dashboard, and the OpenLDAP mark belongs to the
OpenLDAP Foundation:

```
/Users/balu/OiO/repos/vibhuvioio.github.io/public/img/logo.png     # 500x500 PNG, transparent
```

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
   - **Data source**: `Prometheus`
   - **Data source description**: already carried in the upload file's `__inputs`,
     so the import dialog tells the user about the `cluster` label requirement
   - **Description**: use the block below
   - **Logo**: the vibhuvioio brand mark (path in the table above)
   - **Screenshots**: upload the two PNGs
   - **README**: paste `dashboards/catalog-README.md`
5. **Save and Publish**. The public page can 404 for several hours before it
   appears. Later edits use **Submit** on the same page, which also updates
   screenshots, logo and README.

## Description (paste into the metadata form)

> OpenLDAP 2.6 monitoring for the vibhuvioio/openldap-exporter. Replication
> topology and convergence via contextCSN, syncrepl agreement links, per-node
> entry counts and replication lag, plus LMDB map usage, cn=Monitor operations,
> connections, threads and TLS expiry.

## README (paste into the listing)

The text is maintained once, in `dashboards/catalog-README.md` — paste that file's
contents into the README field. It is not duplicated here, because a second copy
drifts.

Before publishing, confirm the screenshot URL inside it actually resolves:

```
https://raw.githubusercontent.com/VibhuviOiO/openldap-exporter/main/dashboards/screenshots/openldap-overview.png
```

That path only works once `dashboards/screenshots/` is committed and pushed to
`main`.

## After publishing

Put the resulting URL in `README.md` and `docs/PROMETHEUS.md`:

```
https://grafana.com/grafana/dashboards/<id>-openldap/
```
