# Controller credentials & minimal RAM policy (P3-CAPA.30)

The CAPA controller authenticates to Alibaba Cloud to manage worker ECS instances.
It resolves a credential in this order (`pkg/client/capi.go` `resolveCredential`):

1. **Explicit ECS RAM role** — env `ALIBABA_CLOUD_ECS_METADATA=<role-name>`.
2. **RAM RoleArn (AssumeRole)** — env `ALIBABA_CLOUD_ROLE_ARN` together with a base
   `ALIBABA_CLOUD_ACCESS_KEY_ID` / `_SECRET`. Optional `ALIBABA_CLOUD_ROLE_SESSION_NAME`
   (default `capa-controller`) and `ALIBABA_CLOUD_ROLE_SESSION_EXPIRATION` (seconds,
   default 3600). The base key needs only `sts:AssumeRole`; the SDK assumes the
   scoped role and auto-refreshes its short-lived STS token.
3. **Static AccessKey** — env `ALIBABA_CLOUD_ACCESS_KEY_ID` / `_SECRET` (or the older
   `ALIBABACLOUD_*` spelling). Dev / non-ECS opt-in.
4. **Auto-discovered ECS RAM role** (default) — the SDK reads the instance role from
   the metadata service.

The resolved mode (never the secret) is logged once at startup
(`Resolved Alibaba Cloud credential mode`).

> **Workload identity (RRSA/OIDC).** The mature equivalent of AWS IRSA —
> exchanging a projected ServiceAccount OIDC token for a scoped role with no static
> key at all — is **not** wired up: the vendored `alibaba-cloud-sdk-go` (v1.61) has
> no OIDC/AssumeRoleWithOIDC signer. **RoleArn AssumeRole (option 2) is the closest
> supported hardening**: the only long-lived key is a minimal `sts:AssumeRole`
> identity, and the working credential is short-lived + auto-rotated. Full RRSA
> would need the `credentials-go` library + STS plumbing (future work).

### RAM RoleArn (AssumeRole) setup

Put the base key + role ARN in the `alibaba-creds` Secret (the controller reads them
via `envFrom`, so no manifest change is needed):

```
oc -n capa-system create secret generic alibaba-creds \
  --from-literal=ALIBABA_CLOUD_ACCESS_KEY_ID=$BASE_AK \
  --from-literal=ALIBABA_CLOUD_ACCESS_KEY_SECRET=$BASE_SK \
  --from-literal=ALIBABA_CLOUD_ROLE_ARN=acs:ram::<account-id>:role/<capa-role>
```

The **base key**'s policy is just AssumeRole:

```json
{ "Version": "1", "Statement": [
  { "Effect": "Allow", "Action": "sts:AssumeRole", "Resource": "*" } ] }
```

The **assumed role** (`<capa-role>`) carries the minimal service policy below, and its
trust policy authorizes the base key's RAM user/role to assume it.

## Prefer a RAM role (recommended)

Run the controller on an ECS instance that carries a RAM role and set **no**
AccessKey env. Then:

- **Rotation is automatic.** The SDK fetches short-lived STS credentials from the
  metadata service and refreshes them before expiry. There is no long-lived secret
  on disk or in a Kubernetes Secret to rotate or leak.
- Static AccessKeys are the opposite: long-lived, stored in env/Secret, and **not**
  rotated — changing them requires restarting the controller. Use only for dev.

Attach the role to the controller's ECS (or, on OpenShift, the node running the
controller) and grant it the policy below.

## Minimal RAM policy

Grants only what the controller calls to manage worker instances. Tighten `Resource`
to your region/cluster where possible (e.g. `acs:ecs:<region>:<account>:instance/*`)
and add a tag condition (`acs:ResourceTag/sigs.k8s.io/cluster-api-provider-alibaba`)
to scope to this provider's resources.

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ecs:RunInstances",
        "ecs:DescribeInstances",
        "ecs:DescribeInstanceStatus",
        "ecs:DescribeInstanceTypes",
        "ecs:DescribeImages",
        "ecs:DescribeDisks",
        "ecs:StartInstance",
        "ecs:StopInstance",
        "ecs:DeleteInstance",
        "ecs:ModifyInstanceAttribute",
        "ecs:ModifyInstanceMetadataOptions",
        "ecs:AttachInstanceRamRole",
        "ecs:TagResources",
        "ecs:ListTagResources",
        "ecs:UntagResources"
      ],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "vpc:DescribeVSwitches",
        "vpc:DescribeVpcs"
      ],
      "Resource": "*"
    }
  ]
}
```

If `AttachInstanceRamRole` is used to give workers a role (for CCM/CSI), also grant
`ram:PassRole` scoped to that worker role.

### Oversized-Ignition OSS offload (only if enabled)

When `AlibabaCloudCluster.spec.ignitionStorage` is set, the controller offloads
oversized worker Ignition to OSS and hands the instance a presigned pointer. Grant,
**scoped to that one bucket**:

```json
{ "Effect": "Allow",
  "Action": ["oss:PutObject", "oss:GetObject", "oss:DeleteObject"],
  "Resource": ["acs:oss:*:*:<ignition-bucket>/*"] }
```

## Not the controller's job

Boot-image baking (OSS upload + `ImportImage`) and cluster teardown run from the
**operator** host (Ansible), not the controller. Keep those broader permissions on
the operator's credentials — do **not** add them to the controller's RAM role.
