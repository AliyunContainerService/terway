# Terway IPv6 Only

This configuration is for **new ACK BYO dual-stack clusters**. Install Terway
with IPv6 Only from the start. ACK, nodes and Service CIDRs remain dual-stack;
ordinary Pods managed by Terway receive only IPv6. There is no protocol-stack
migration workflow. Host-network Pods retain the node's networking.

## Install

Use the same image build for the daemon, CNI and controlplane. Set the image
registry and tag to the build under test, then create separate environments:

```sh
export TERWAY_IMAGE_REGISTRY=<registry/namespace>
export TERWAY_IMAGE_TAG=<build-tag>
make ack-cluster-byo-dual-cni-ipv6-default
make ack-cluster-byo-dual-cni-ipv6-crd
```

The IPv6-only profiles set the exclusive-ENI and prefix node pools to zero
workers; only the two ordinary shared-ENI workers run the test workload.
New IPv6-only workdirs use the verified ACK version `1.35.7-aliyun.1`; existing
workdirs and the shared Terraform version default are preserved.
Each invocation uses an isolated Terraform workdir under
`hack/terraform/ack/runs/`. Configure region, credentials, worker instance types
and counts through the existing Terraform configuration. Use at least two Linux
workers with IPv6-capable ECS instances and IPv6-enabled VPC/vSwitches. New ENIs
still require an available IPv4 address for their mandatory Primary IP.

The profiles set ACK `ip_stack=dual` and pass `--ip-stack ipv6 --ipam default`
or `--ip-stack ipv6 --ipam crd` to `deploy-terway.sh`. For manual Helm installs:

```yaml
centralizedIPAM: false # true for crd, false for default
terway:
  ipStack: ipv6
  serviceCIDR: "192.168.0.0/16,fd00:1234::/112" # actual ACK Service CIDRs
terwayControlplane:
  ipStack: ipv6
```

The daemon and controlplane must agree on IPv6 Only. Node-specific configuration
cannot change the cluster's IPv6-only setting. `min_pool_size`, `max_pool_size`
and per-node Pod IP capacity count IPv6 addresses. No auxiliary IPv4 addresses
are requested for Pods. ECS Primary IPv4 remains ENI metadata and is released
only when its ENI is deleted.

## Optional cluster-service checks

The default acceptance is scoped to CNI on ACK dual-stack. DNS and Kubernetes
API IPv6 frontends are not required by this CNI-only test. To additionally run
`--check-cluster-services`, first satisfy these prerequisites:

- Provide a DNS nameserver reachable over IPv6 in the Pod's resolver settings.
  Check kubelet cluster DNS and the DNS Service's IPv6 frontend; merely enabling
  dual-stack Service CIDRs does not convert existing IPv4 Services.
- Provide a reachable IPv6 API endpoint for ordinary Pods and system components.
  The optional check tests the IPv6 frontend of `default/kubernetes` using the
  projected service-account token and CA. An IPv4-only frontend is an environment
  prerequisite failure, not a reason to allocate an IPv4 to the Pod.
- Explicitly create test Services with `ipFamilyPolicy: SingleStack` and
  `ipFamilies: [IPv6]`. Do not rely on the default Service address family.
- Permit IPv6 traffic between test workers and Pod vSwitches, including ICMPv6
  for neighbor discovery and path MTU discovery.

The acceptance scope is Linux ENIMultiIP with default and CRD IPAM. It does not
qualify Windows, ERDMA, trunk/fixed IP/multiple interfaces, Prefix Delegation,
NetworkPolicy, public IPv6 bandwidth, load balancers or NAT64/DNS64. Use ordinary
shared ENI nodes and leave those features disabled in these test environments.

## Verification

Run on a Linux development host with the prerequisites from the repository:

```sh
make fmt
make lint-fix
make lint
make vet
make test
```

`make test` includes privileged datapath tests and envtest. macOS feedback does
not replace Linux verification. The IPv6-only tests cover configuration merging,
unequal IPv4/IPv6 quotas, cloud Primary IPv4 handling, missing and partial IPv6
responses, local recovery, CRD allocation and reclaim, node capacity/conditions,
CNI config parsing, and IPvlan/policy-route setup and teardown.

Run the dedicated ACK test for **each** IPAM mode. It requires kubectl, local
Python 3 and a test container image containing Python 3 (no third-party Python
packages). Choose a Pod count greater than the instance's IPv6-per-ENI quota,
but below its node and kubelet Pod limits:

```sh
export KUBECONFIG=<workdir/kubeconfig-file>
python3 hack/ipv6-only-e2e.py \
  --ipam default \
  --image <python3-test-image> \
  --pods-per-node <count-greater-than-IPv6-per-ENI-quota> \
  --artifacts <result-directory>
```

Repeat with `--ipam crd` against the second cluster. The runner uses a unique
namespace and deletes it afterward. It restarts the Terway daemonset and, in CRD
mode, the controlplane deployment. It does not change the cluster configuration
or use the broad E2E suite's configuration-reset hooks.

Assertions include one IPv6 in `status.podIPs`, no IPv4 on non-loopback Pod
interfaces or in Pod routes, unique addresses, TCP/UDP in both directions on the
same and different nodes, IPv6 Service/EndpointSlice,
scale-up, existing Pod connectivity after restart, and allocation after deletion
and recreation. DNS over IPv6 and API access are checked only with
`--check-cluster-services`. Pod, event and node snapshots are collected for diagnosis.

Complete the following cloud-side acceptance checks as well; a connectivity
runner alone does not prove ENI lifecycle correctness:

| Scenario | Required evidence |
| --- | --- |
| Scale beyond one ENI | More than one ENI on each test worker; only requested IPv6 plus one Primary IPv4 on each newly created ENI |
| Allocate and warm pool | No auxiliary IPv4 assignment API calls; capacity and idle counts use IPv6 |
| Delete workload | Pod bindings disappear; IPv6 and empty ENIs converge to the configured pool watermarks after the reclaim interval |
| Restart | Existing bindings survive; no duplicate allocation, IPv4 pool entries, or repeated ENI creation |
| IPv4 shortage | Existing ENIs can add IPv6; new ENI creation reports shortage when a Primary IPv4 cannot be allocated |
| IPv6 failure | Allocation stays pending or fails explicitly, never succeeds with only Primary IPv4; retry/cleanup leaves no orphan ENIs |
| IPv4/dual regression | Existing repository tests pass without changing the established IPv4/dual configuration semantics |

Do not report full acceptance until both IPAM environments, Linux `make test`,
and the cloud resource lifecycle checks pass. Preserve the build tag, instance
quotas, rendered values, API request evidence and test results with each run.
