# Custom CNI Chain

## Overview

Terway will set CNI config on the startup. The config (`/etc/cni/net.d/10-terway.conf`) looks like:

```json
{
  "cniVersion": "0.4.0",
  "name": "terway",
  "type": "terway",
  "eniip_virtual_type": "IPVlan"
}
```

## Configuration

The recommended configuration is `cni_chain`. It contains only the plugins
that run after Terway, so the Terway plugin configuration does not need to be
copied into a conflist.

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: eni-config
  namespace: kube-system
data:
  cni_chain: |
    - type: portmap
      capabilities:
        portMappings: true
      externalSetMarkChain: KUBE-MARK-MASQ
```

`cni_chain` must be a YAML list. Every entry must have a non-empty `type`.
Terway owns the top-level `name`, `cniVersion`, and `plugins` fields, and the
Terway plugin itself, so those fields cannot be specified in the list. This
configuration also works on exclusive ENI nodes.

### Node-specific chain

Use the existing `terway-config` node label to select a ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: eni-config-node-a
  namespace: kube-system
data:
  cni_chain: |
    - type: tuning
      sysctl:
        net.ipv6.conf.all.disable_ipv6: "1"
```

```shell
kubectl label node <node-name> terway-config=eni-config-node-a
```

When the selected ConfigMap contains `cni_chain`, it replaces the global
chain. If it does not contain the field, the global chain is inherited. Set
the value to `[]` to disable the global chain on selected nodes. Restart the
Terway pod on affected nodes after changing the label or configuration.

### Legacy conflist

Terway will take the cni config in this order `10-terway.conflist` then `10-terway.conf`.
If you need to use custom CNI plugin, you need to add the new `10-terway.conflist` field.

`10-terway.conflist` is retained for backward compatibility. When `cni_chain`
is configured, it takes precedence over the legacy conflist.

> Please note that there is no guarantee that custom plugin functionality will work.

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: eni-config
  namespace: kube-system
data:
  10-terway.conflist: |
      {
        "plugins": [
          {
            "type": "terway"
          },
          {
            "type": "cilium-cni"
          },
          {
            "type": "portmap",
            "capabilities": {"portMappings": true},
            "externalSetMarkChain":"KUBE-MARK-MASQ"
          }
        ]
      }

  10-terway.conf: |
    {
      "cniVersion": "0.4.0",
      "name": "terway",
      "type": "terway"
    }


```
