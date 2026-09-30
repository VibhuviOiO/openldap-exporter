#!/usr/bin/env python3
"""Regenerate the Grafana community-catalog export from the source dashboard.

The catalog accepts the Classic model only, with datasource inputs so importers
are asked which Prometheus to use. `dashboards/openldap.json` is provisioned
into the monitoring stack and references `${datasource}` directly, which is fine
locally and wrong for import - hence this conversion.

Usage:  python3 dashboards/export-for-catalog.py
Writes: dashboards/openldap-catalog.json
"""
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent
SRC = HERE / "openldap.json"
OUT = HERE / "openldap-catalog.json"

# Panel type -> display name, for __requires.
PANEL_NAMES = {
    "timeseries": "Time series",
    "stat": "Stat",
    "gauge": "Gauge",
    "table": "Table",
    "piechart": "Pie chart",
    "state-timeline": "State timeline",
    "bargauge": "Bar gauge",
    "nodeGraph": "Node graph",
}


def main() -> int:
    dashboard = json.loads(SRC.read_text())

    # 1. Every datasource reference becomes the import input. Do it on the text
    #    so it catches both the compact and spaced JSON spellings.
    raw = json.dumps(dashboard)
    raw = raw.replace('"uid": "${datasource}"', '"uid": "${DS_PROMETHEUS}"')
    raw = raw.replace('"uid":"${datasource}"', '"uid":"${DS_PROMETHEUS}"')
    dashboard = json.loads(raw)

    # 2. __inputs replaces the datasource template variable.
    before = dashboard["templating"]["list"]
    dashboard["templating"]["list"] = [
        v for v in before if v.get("type") != "datasource"
    ]
    removed = len(before) - len(dashboard["templating"]["list"])

    # 3. The import contract.
    dashboard["__inputs"] = [
        {
            "name": "DS_PROMETHEUS",
            "label": "Prometheus",
            "description": "",
            "type": "datasource",
            "pluginId": "prometheus",
            "pluginName": "Prometheus",
        }
    ]

    # 4. What the dashboard needs in order to render.
    panel_types = set()

    def walk(panels):
        for panel in panels:
            kind = panel.get("type")
            if kind and kind != "row":
                panel_types.add(kind)
            walk(panel.get("panels", []))

    walk(dashboard["panels"])
    dashboard["__requires"] = [
        {"type": "grafana", "id": "grafana", "name": "Grafana", "version": "11.0.0"},
        {
            "type": "datasource",
            "id": "prometheus",
            "name": "Prometheus",
            "version": "1.0.0",
        },
    ]
    for kind in sorted(panel_types):
        dashboard["__requires"].append(
            {
                "type": "panel",
                "id": kind,
                "name": PANEL_NAMES.get(kind, kind),
                "version": "",
            }
        )

    # A catalog upload is an import, not a version of the local dashboard.
    dashboard["version"] = 0

    OUT.write_text(json.dumps(dashboard, indent=2) + "\n")

    leftover = json.dumps(dashboard).count("${datasource}")
    print(f"  removed {removed} datasource variable(s)")
    print(f"  panel types: {', '.join(sorted(panel_types))}")
    print(f"  unresolved ${{datasource}} references: {leftover}")
    print(f"  wrote {OUT.relative_to(HERE.parent)}")

    if leftover:
        print("ERROR: a ${datasource} reference survived the rewrite", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
