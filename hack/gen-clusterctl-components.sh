#!/usr/bin/env bash
# Generate clusterctl `infrastructure-components.yaml` for this provider.
#
# The canonical deployment manifests (CRDs + controller + RBAC + webhooks) live in
# the sibling alibaba-openshift repo's custom_manifests/ (the same files Phase 08
# applies). Rather than keep a third hand-maintained copy, this script assembles
# them and stamps every object with the clusterctl provider label so `clusterctl`
# can track/move/delete the provider.
#
# Usage:
#   hack/gen-clusterctl-components.sh [OUT]
# Default OUT = out/infrastructure-components.yaml. Then place it (with
# metadata.yaml + templates/cluster-template.yaml) under the clusterctl override
# layout — see docs/CLUSTERCTL.md.
#
# NOTE: this is the interim generator. The structural follow-up is to make the
# provider repo's config/ the single kustomize source and wire `make release`.
set -euo pipefail

PROVIDER_LABEL="infrastructure-alibabacloud"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
manifests="${CAPA_MANIFESTS_DIR:-$here/../alibaba-openshift/custom_manifests}"
out="${1:-$here/out/infrastructure-components.yaml}"
# Optional: override the controller image. The canonical 02-capa-controller.yaml
# pins a TAG placeholder (ansible sed-overrides it at deploy time); clusterctl has
# no such sed step, so pass CAPA_IMAGE to bake a real, pullable image into the
# generated components (e.g. for the kind smoke). Empty = leave manifest image as-is.
capa_image="${CAPA_IMAGE:-}"

for f in 02-capa-crds.yaml 02-capa-controller.yaml 02-capa-webhooks.yaml; do
  [ -f "$manifests/$f" ] || { echo "missing $manifests/$f (set CAPA_MANIFESTS_DIR)" >&2; exit 1; }
done

mkdir -p "$(dirname "$out")"
python3 - "$manifests" "$PROVIDER_LABEL" "$out" "$capa_image" <<'PY'
import sys, yaml
manifests, label, out, capa_image = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
files = ["02-capa-crds.yaml", "02-capa-controller.yaml", "02-capa-webhooks.yaml"]
docs = []
for f in files:
    with open(f"{manifests}/{f}") as fh:
        for d in yaml.safe_load_all(fh):
            if not d:
                continue
            # clusterctl tracks provider objects by this label.
            meta = d.setdefault("metadata", {})
            meta.setdefault("labels", {})["cluster.x-k8s.io/provider"] = label
            # Bake a real controller image when CAPA_IMAGE is set (kind smoke / release).
            if capa_image and d.get("kind") == "Deployment":
                containers = (d.get("spec", {}).get("template", {})
                              .get("spec", {}).get("containers", []))
                for c in containers:
                    if c.get("name") == "manager":
                        c["image"] = capa_image
            docs.append(d)
with open(out, "w") as fh:
    yaml.dump_all(docs, fh, default_flow_style=False, sort_keys=False)
print(f"wrote {len(docs)} objects -> {out}" + (f" (manager image -> {capa_image})" if capa_image else ""))
PY
