#!/usr/bin/env bash
# Generate clusterctl `infrastructure-components.yaml` for this provider.
#
# The deployable manifests (CRDs + RBAC + controller + webhooks) come from the
# single kustomize SSOT in this repo's config/default — the SAME source ansible
# (08-deploy-post-install) and the OLM bundle CRDs build from. This script renders
# it with `kubectl kustomize`, bakes a real controller image when CAPA_IMAGE is
# set (clusterctl has no ansible sed step), and stamps every object with the
# clusterctl provider label so `clusterctl` can track/move/delete the provider.
#
# Usage:
#   hack/gen-clusterctl-components.sh [OUT]
# Default OUT = out/infrastructure-components.yaml. Then place it (with
# metadata.yaml + templates/cluster-template.yaml) under the clusterctl override
# layout — see docs/CLUSTERCTL.md.
set -euo pipefail

PROVIDER_LABEL="infrastructure-alibabacloud"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
kustomize_dir="${CAPA_KUSTOMIZE_DIR:-$here/config/default}"
out="${1:-$here/out/infrastructure-components.yaml}"
# Optional: override the controller image (config/default pins a TAG placeholder;
# clusterctl has no ansible sed step). Empty = leave the kustomize image as-is.
capa_image="${CAPA_IMAGE:-}"

command -v kubectl >/dev/null 2>&1 || { echo "need 'kubectl' on PATH (for kubectl kustomize)" >&2; exit 1; }
[ -f "$kustomize_dir/kustomization.yaml" ] || { echo "missing $kustomize_dir/kustomization.yaml" >&2; exit 1; }

mkdir -p "$(dirname "$out")"
rendered="$(mktemp)"
trap 'rm -f "$rendered"' EXIT
kubectl kustomize "$kustomize_dir" > "$rendered"
python3 - "$PROVIDER_LABEL" "$out" "$capa_image" "$rendered" <<'PY'
import sys, yaml
label, out, capa_image, rendered = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
docs = []
for d in yaml.safe_load_all(open(rendered)):
    if not d:
        continue
    # clusterctl tracks provider objects by this label.
    meta = d.setdefault("metadata", {})
    meta.setdefault("labels", {})["cluster.x-k8s.io/provider"] = label
    # Bake a real controller image when CAPA_IMAGE is set (kind smoke / release).
    if capa_image and d.get("kind") == "Deployment":
        for c in d.get("spec", {}).get("template", {}).get("spec", {}).get("containers", []):
            if c.get("name") == "manager":
                c["image"] = capa_image
    docs.append(d)
with open(out, "w") as fh:
    yaml.dump_all(docs, fh, default_flow_style=False, sort_keys=False)
print(f"wrote {len(docs)} objects -> {out}" + (f" (manager image -> {capa_image})" if capa_image else ""))
PY
