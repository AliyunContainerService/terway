# eip

## Feature Description

This document aims to explain to users that the EIP (Elastic IP) functionality in Terway is deprecated and no longer maintained. Users can use the EIP functionality in ACK Extend Network Controller as an alternative.

Why the EIP Functionality is Deprecated
Due to technological advancements and evolving business requirements, the Terway team has decided to optimize and upgrade the EIP functionality. In order to provide better network capabilities and enhance user experience, the Terway team has separated the EIP functionality from Terway and integrated it into [ACK Extend Network Controller](https://help.aliyun.com/zh/ack/product-overview/ack-extend-network-controller).

Using the EIP Functionality in ACK Extend Network Controller
After deprecation of the EIP functionality in Terway, users can utilize the EIP functionality in ACK Extend Network Controller. ACK Extend Network Controller is an advanced container network plugin provided by Alibaba Cloud for extending Kubernetes network capabilities. It offers the Elastic IP (EIP) functionality and seamlessly integrates with other network functionalities in Terway.

## How migration works

ACK Extend Network Controller usr CRD to trace the lifecycle of EIPs.
When enable the EIP migration feature in Terway, Terway will create a `podEIP` object for each EIP in the cluster.
So ACK Extend Network Controller can get the EIPs information from the `podEIP` object and manage the EIPs. Also Terway will no longer manage the EIPs.

### pod with specific EIP id

For pod specific EIP id, the EIP is provided by user. The `spec.allocationType.type` of `podEIP` cr would be `Static` .
For pod not specific EIP id, the EIP is create by terway , and the `spec.allocationType.type` of `podEIP` cr would be `Auto` .

### non-stateful workload

For non-stateful workload, the `spec.allocationType.releaseStrategy` of `podEIP` cr would be `Follow` .

### stateful workload

For stateful workload, the `spec.allocationType.releaseStrategy` of `podEIP` cr would be `TTL` .

## Migrate to ACK Extend Network Controller

1. Make sure ACK Extend Network Controller is **NOT** installed in your cluster. If ACK Extend Network Controller is already installed, please uninstall it first.
2. Upgrade Terway to the latest version.
3. Modify the Terway configuration file to enable the EIP migration functionality in Terway.
   - Set the value of the `enable_eip_migrate` parameter to `true` .
   - Keep the value of the `enable_eip_pool` parameter to `"true"` .

    ```yaml
    apiVersion: v1  
    data:
      10-terway.conf: |
        {
          "type": "terway"
        }
      eni_conf: |
        {
          "version": "1",
    
          "enable_eip_pool": "true",
          "enable_eip_migrate": true,
          "vswitch_selection_policy": "ordered"
        }
    kind: ConfigMap
    metadata:
      name: eni-config
      namespace: kube-system
    ```

4. Make sure to restart the Terway daemonset after modifying the Terway configuration file.
5. After the Terway daemonset is restarted, Terway will automatically migrate the EIPs in your cluster to ACK Extend Network Controller. You can check the migration status in the Terway log.
6. Check the `podEIP` CRD object in your cluster to see if the config is successfully created. The CRD object has the same name and namespace as the Pod.
7. Start the [ACK Extend Network Controller](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/associate-an-eip-with-a-pod-1) with eip function.
8. Turn off terway eip function.
   - Set the value of the `enable_eip_migrate` parameter to `false` or delete it.
   - Set the value of the `enable_eip_pool` parameter to `"false"` or delete it.
   - Restart the Terway daemonset.

By following the steps mentioned above, you can successfully replace the deprecated EIP functionality in Terway with the EIP functionality in ACK Extend Network Controller. If you have any questions or need further assistance, please feel free to contact us.
