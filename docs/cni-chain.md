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

Terway will take the cni config in this order `10-terway.conflist` then `10-terway.conf`.
If you need to use custom CNI plugin, you need to add the new `10-terway.conflist` field.

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
