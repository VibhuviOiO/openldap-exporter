# OpenLDAP 2.6 — Replication, Convergence & Health

Grafana dashboard for [vibhuvioio/openldap-exporter](https://github.com/VibhuviOiO/openldap-exporter),
covering OpenLDAP 2.6 replication and server health.

**Setup and configuration:** [vibhuvioio.com — Prometheus Exporter](https://vibhuvioio.com/openldap-docker/observability/prometheus-exporter/)

![Overview](https://raw.githubusercontent.com/VibhuviOiO/openldap-exporter/main/dashboards/screenshots/openldap-overview.png)

## Required: a `cluster` label

The dashboard groups everything by a `cluster` label that **the exporter does not
emit**. Your Prometheus must attach it, one per exporter instance:

```yaml
scrape_configs:
  - job_name: openldap
    scrape_interval: 30s
    static_configs:
      - targets: ['openldap-exporter:9330']
        labels: { cluster: prod }
      - targets: ['openldap-exporter-alt:9331']
        labels: { cluster: staging }
```

Run **one exporter instance per directory**. The `entries:` searches in
`openldap-exporter.yml` are global to an instance, not per target, and each
directory has its own base DN — so a single instance cannot serve two of them.

Without the label the `cluster`, `group` and `target` variables come up empty and
most panels show no data.

## Variables

| Variable | Meaning |
|---|---|
| `cluster` | one per exporter instance, from the label above |
| `group` | the exporter's `group:` for a target — the nodes that must agree |
| `target` | a single server |

## Reading a replication problem

The panels are ordered so each step isolates the cause:

1. **Nodes up** — is anything answering at all.
2. **Links healthy** — did the providers observe their consumers connect.
3. **In sync** — do the `contextCSN` values agree.
4. **Replication lag by sid** — how far behind, per sid.
5. **Consumer links seen by providers** — which node's link is dead while its own
   socket still looks healthy. A `0` here with a green "Up" elsewhere is the
   failure this dashboard exists to catch.
6. **Records / Change sets / Last change** per node — did the data actually
   arrive.

`Last change` is the newest `contextCSN` timestamp for a node. Identical values
across a cluster mean the nodes converged on the same newest change; a node that
has fallen behind shows an older timestamp. It is the strongest convergence
signal on the page, because it comes from the data rather than from replication's
own bookkeeping.

## Panels that are empty when healthy

- **Sids missing per target** — only lists sids a node is missing.
- **TLS certificate days remaining** — needs
  `openldap_tls_certificate_expiry_seconds`, which requires TLS on the target.

Empty is the good outcome here, not a broken panel.

## Multiprovider meshes

Every node in a multi-provider (N-way) mesh is both provider and consumer, so the
`role` label is `provider` throughout and the dashboard hides that column. The
label is still on the metrics: the shipped alert rules filter on
`openldap_entries{role="provider"}` to separate a prod node from a QA replica in
one-way setups.

## One more thing worth knowing

Two providers that bootstrapped independently each hold a `contextCSN` for their
own sid only. Until each accepts one change from the other's sid, their CSN sets
differ and a sync check reports the pair as not converged — even though both are
healthy. One write against each node closes it. If you built the cluster by
loading the same LDIF into both nodes, write something to the second node before
trusting the sync panels.

---

Full walkthrough: [vibhuvioio.com — Grafana Dashboard](https://vibhuvioio.com/openldap-docker/observability/grafana-dashboard/)
