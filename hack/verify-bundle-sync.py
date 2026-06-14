#!/usr/bin/env python3
"""Assert the OLM bundle stays in sync — turns the G12 manual sync into an
enforced invariant.

Two checks:

1. DEPLOYMENT SYNC — the CSV install-strategy Deployment spec must match the
   canonical controller Deployment in the kustomize SSOT
   (config/manager/deployment.yaml), field-for-field, EXCEPT the
   webhook serving-cert volume + volumeMount and the container image:
     * the cert volume is intentionally absent under OLM — OLM injects the
       serving cert (tls.crt/tls.key at /tmp/k8s-webhook-server/serving-certs,
       confirmed against OLM certresources.go) from spec.webhookdefinitions, so
       copying the OpenShift service-ca volume here would be wrong;
     * the image differs by design (the ansible manifest pins a TAG placeholder
       that 08-deploy sed-overrides; the CSV pins the real release image).
   Any OTHER drift fails — so a future change to the controller Deployment that
   isn't mirrored into the CSV is caught here.

2. VERSION SSOT — within the CSV, the four places a version appears must agree:
   metadata.name suffix, spec.version, annotations.containerImage tag, and the
   manager container image tag. (No external SSOT yet; this at least makes a
   half-done manual bump fail loudly.)

Usage: hack/verify-bundle-sync.py
Override the canonical manifest with CAPA_CONTROLLER_MANIFEST=/path.

The canonical controller Deployment is config/manager/deployment.yaml — the same
kustomize SSOT ansible and clusterctl consume.
"""
import os
import sys
import copy

import yaml

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
CSV = os.path.join(
    REPO, "bundle", "manifests",
    "cluster-api-provider-alibabacloud.clusterserviceversion.yaml",
)
CONTROLLER = os.environ.get(
    "CAPA_CONTROLLER_MANIFEST",
    os.path.join(REPO, "config", "manager", "deployment.yaml"),
)

errors = []
def fail(msg): errors.append(msg)


def load_csv():
    with open(CSV) as fh:
        return yaml.safe_load(fh)


def canonical_deployment():
    with open(CONTROLLER) as fh:
        for d in yaml.safe_load_all(fh):
            if d and d.get("kind") == "Deployment":
                return d
    raise SystemExit(f"no Deployment in {CONTROLLER}")


def manager(spec):
    for c in spec["template"]["spec"]["containers"]:
        if c["name"] == "manager":
            return c
    raise SystemExit("no 'manager' container")


def normalize(spec):
    """Drop the parts that are expected to differ (OLM-managed cert + image)."""
    s = copy.deepcopy(spec)
    pod = s["template"]["spec"]
    vols = [v for v in pod.get("volumes", []) if v.get("name") != "cert"]
    if vols:
        pod["volumes"] = vols
    else:
        pod.pop("volumes", None)
    m = manager(s)
    mounts = [vm for vm in m.get("volumeMounts", []) if vm.get("name") != "cert"]
    if mounts:
        m["volumeMounts"] = mounts
    else:
        m.pop("volumeMounts", None)
    m.pop("image", None)
    return s


def diff(a, b, path=""):
    """Yield human-readable differences between two nested structures."""
    if isinstance(a, dict) and isinstance(b, dict):
        for k in sorted(set(a) | set(b)):
            p = f"{path}.{k}"
            if k not in a:
                yield f"  only in CSV:        {p} = {b[k]!r}"
            elif k not in b:
                yield f"  only in canonical:  {p} = {a[k]!r}"
            else:
                yield from diff(a[k], b[k], p)
    elif isinstance(a, list) and isinstance(b, list):
        if len(a) != len(b):
            yield f"  list length differs at {path}: canonical={len(a)} csv={len(b)}"
        for i, (x, y) in enumerate(zip(a, b)):
            yield from diff(x, y, f"{path}[{i}]")
    elif a != b:
        yield f"  {path}: canonical={a!r}  csv={b!r}"


# ── check 1: deployment sync ────────────────────────────────────────────────
csv = load_csv()
csv_dep = csv["spec"]["install"]["spec"]["deployments"]
if len(csv_dep) != 1 or csv_dep[0]["name"] != "capa-controller-manager":
    fail(f"unexpected CSV deployments: {[d.get('name') for d in csv_dep]}")
canon = normalize(canonical_deployment()["spec"])
mine = normalize(csv_dep[0]["spec"])
d = list(diff(canon, mine, "spec"))
if d:
    fail("CSV deployment spec drifted from config/manager/deployment.yaml:\n" + "\n".join(d))

# guard: the cert volume really is the only thing the CSV drops (so the
# allowlist can't silently hide a future, different omission).
canon_vol_names = {v["name"] for v in
                   canonical_deployment()["spec"]["template"]["spec"].get("volumes", [])}
csv_vol_names = {v["name"] for v in
                 csv_dep[0]["spec"]["template"]["spec"].get("volumes", [])}
dropped = canon_vol_names - csv_vol_names
if dropped - {"cert"}:
    fail(f"CSV drops unexpected volumes (only 'cert' is OLM-managed): {dropped}")

# ── check 2: version SSOT inside the CSV ────────────────────────────────────
def tag_ver(image):
    return image.rsplit(":", 1)[-1].lstrip("v") if image else None

name_ver = csv["metadata"]["name"].rsplit(".v", 1)[-1]
spec_ver = str(csv["spec"]["version"])
ann_ver = tag_ver(csv["metadata"].get("annotations", {}).get("containerImage"))
img_ver = tag_ver(manager(csv_dep[0]["spec"])["image"])
versions = {"metadata.name": name_ver, "spec.version": spec_ver,
            "annotations.containerImage": ann_ver, "manager image": img_ver}
if len(set(versions.values())) != 1:
    fail("CSV version is inconsistent across fields:\n" +
         "\n".join(f"    {k} = {v}" for k, v in versions.items()))

# ── result ──────────────────────────────────────────────────────────────────
if errors:
    print("BUNDLE SYNC: FAIL")
    for e in errors:
        print(" -", e)
    sys.exit(1)
print(f"BUNDLE SYNC: OK (deployment in sync; version {spec_ver} consistent across "
      "name/version/containerImage/image)")
