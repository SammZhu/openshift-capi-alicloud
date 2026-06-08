# Controller credentials & minimal RAM policy (P3-CAPA.30)

The CAPA controller authenticates to Alibaba Cloud to manage worker ECS instances.
It resolves a credential in this order (`pkg/client/capi.go` `resolveCredential`):

1. **Explicit ECS RAM role** — env `ALIBABA_CLOUD_ECS_METADATA=<role-name>`.
2. **Static AccessKey** — env `ALIBABA_CLOUD_ACCESS_KEY_ID` / `_SECRET` (or the older
   `ALIBABACLOUD_*` spelling). Dev / non-ECS opt-in.
3. **Auto-discovered ECS RAM role** (default) — the SDK reads the instance role from
   the metadata service.

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
