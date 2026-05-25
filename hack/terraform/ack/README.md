# ACK E2E Cluster Terraform

Terraform module for spinning up ACK managed clusters used by Terway's end-to-end tests. Wrapped by `e2e-cluster.sh` which materializes an isolated workdir per run so multiple clusters can coexist on the same module.

## Profiles

| profile      | ip stack | CNI install path                                                                                                  | use case                  |
|--------------|----------|-------------------------------------------------------------------------------------------------------------------|---------------------------|
| `byo-ipv4`   | ipv4     | `cluster_addons` does **not** include terway. After cluster creation, `deploy-terway.sh` helm-installs the chart. | dev / open-source install |
| `ack-ipv4`   | ipv4     | `cluster_addons` includes `terway-eniip`. ACK installs terway-eniip daemonset automatically.                      | E2E feature tests         |
| `ack-dual`   | dual     | Same as `ack-ipv4` (only ACK mode is allowed for dual stack).                                                     | dual-stack E2E tests      |
| `byo-dual`   | -        | Rejected. ACK API requires a CNI plugin for dual-stack clusters (`IPv6DependencyNotSatisfied.NetworkPlugin`).     | -                         |

### BYO vs ACK

- **BYO** (Bring Your Own CNI): cluster boots with no CNI; flannel is disabled in `cluster_addons`. Operator runs `deploy-terway.sh` afterwards (or installs any other CNI).
- **ACK**: cluster boots with the `terway-eniip` ACK addon; ACK manages the daemonset lifecycle. In **managed clusters** (`alicloud_cs_managed_kubernetes`, used here) `terway-controlplane` is hosted by Aliyun's control plane and is **not visible** as a `Deployment` in `kube-system`; you only see a placeholder `service/terway-controlplane`.

## Workdir isolation

Each `create` invocation materializes a workdir under `runs/<profile>-<timestamp>/` (override with `--workdir <path>`):

- `*.tf` symlinked from this directory (so module logic stays in one place)
- `terraform.tfvars` copied (so per-run tweaks don't bleed back)
- `.terraform/`, `terraform.tfstate`, `kubeconfig-*`, `terway-values.yaml` are local to the workdir

This means you can run `byo-ipv4` and `ack-ipv4` simultaneously without state collision. `runs/` is git-ignored.

## Quick start

```sh
# Create
make ack-cluster-ack-ipv4
# or:
./e2e-cluster.sh create ack-dual

# List active workdirs
make ack-list

# Get kubeconfig (defaults to most recent run; or WORKDIR=...)
export KUBECONFIG=$(make -s ack-kubeconfig)

# Destroy (defaults to most recent run; or WORKDIR=...)
make ack-destroy WORKDIR=hack/terraform/ack/runs/ack-ipv4-20260519-103015
```

## Manual (without wrapper)

```sh
mkdir -p runs/foo && cd runs/foo
ln -sf ../../*.tf .
cp ../../terraform.tfvars .
terraform init
terraform plan \
  -var=ip_stack=ipv4 \
  -var=cluster_mode=ack \
  -var=service_cidr=192.168.0.0/16
terraform apply ...
```

## Variables of interest

- `cluster_mode` (`"byo"` | `"ack"`): selects whether `cluster_addons` injects `terway-eniip`.
- `ip_stack` (`"ipv4"` | `"dual"`): VPC and cluster IP family.
- `service_cidr`: comma-separated `<ipv4-cidr>,<ipv6-cidr>` for dual stack; single CIDR for ipv4.
- `cluster_addons` is not a variable any more — it is computed in `locals.all_addons` from `cluster_mode` (see `terraform_e2e.tf`).

Two lifecycle preconditions enforce ACK API constraints before apply:

1. `byo + dual` is rejected (ACK requires CNI for dual stack).
2. `dual` requires `service_cidr` to contain both IPv4 and IPv6 CIDRs.

## Files

- `terraform_e2e.tf` — provider variables, VPC/vSwitch resources, cluster + node pools, IP-prefix configmap.
- `vswitch_cidr_reservation.tf` — IPv4 `/18` and IPv6 `/66` Prefix CIDR reservations on Pod vSwitches.
- `provider.tf` — alicloud / random / kubernetes provider config.
- `terraform.tfvars` — defaults; per-profile values are injected by `e2e-cluster.sh` via `-var=`.
- `e2e-cluster.sh` — wrapper: `create | destroy | kubeconfig | list`.
- `deploy-terway.sh` — helm-installs terway-eniip + terway-controlplane (BYO mode only). Reads cluster info from `terraform output`.
