package main

import (
	"flag"
	"fmt"
	"os"
	"text/template"
)

// ConfigMapParams holds the parameters for rendering the eni-config ConfigMap template.
// Only Datapath and DisableNetworkPolicy vary by test config mode (A/B/C).
// The rest are cluster-specific values extracted from Terraform state.
type ConfigMapParams struct {
	Datapath             bool   // true: enable eniip_virtual_type (IPVlan/datapathv2); false: veth mode
	DisableNetworkPolicy string // "true" to disable NP, "false" to enable
	PodVswitchId         string // JSON map string, e.g. {"cn-hangzhou-j":["vsw-xxx"]}
	ClusterID            string // ACK cluster ID
	ServiceCIDR          string // Kubernetes service CIDR
	SecurityGroupId      string // Security group ID
	IPStack              string // "ipv4" or "dual"
	RegionID             string // ACK region ID, e.g. cn-hangzhou
	VPCID                string // VPC ID, e.g. vpc-xxx
}

func main() {
	var (
		templateFile    string
		datapath        bool
		disableNP       string
		podVswitchId    string
		clusterID       string
		serviceCIDR     string
		securityGroupID string
		ipStack         string
		regionID        string
		vpcID           string
	)

	flag.StringVar(&templateFile, "template", "", "path to configmap.yaml.tmpl (required)")
	flag.BoolVar(&datapath, "datapath", false, "enable datapath mode (IPVlan/datapathv2)")
	flag.StringVar(&disableNP, "disable-np", "true", "disable_network_policy value (true=disable, false=enable)")
	flag.StringVar(&podVswitchId, "pod-vswitch-id", "", `vswitch IDs as JSON map, e.g. '{"cn-hangzhou-j":["vsw-xxx"]}'`)
	flag.StringVar(&clusterID, "cluster-id", "", "ACK cluster ID")
	flag.StringVar(&serviceCIDR, "service-cidr", "", "Kubernetes service CIDR")
	flag.StringVar(&securityGroupID, "security-group-id", "", "security group ID")
	flag.StringVar(&ipStack, "ip-stack", "ipv4", "IP stack: ipv4 or dual")
	flag.StringVar(&regionID, "region-id", "cn-hangzhou", "ACK region ID")
	flag.StringVar(&vpcID, "vpc-id", "", "VPC ID")
	flag.Parse()

	if templateFile == "" {
		fmt.Fprintln(os.Stderr, "error: --template is required")
		os.Exit(1)
	}

	tmpl, err := template.ParseFiles(templateFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing template %s: %v\n", templateFile, err)
		os.Exit(1)
	}

	params := ConfigMapParams{
		Datapath:             datapath,
		DisableNetworkPolicy: disableNP,
		PodVswitchId:         podVswitchId,
		ClusterID:            clusterID,
		ServiceCIDR:          serviceCIDR,
		SecurityGroupId:      securityGroupID,
		IPStack:              ipStack,
		RegionID:             regionID,
		VPCID:                vpcID,
	}

	if err := tmpl.Execute(os.Stdout, params); err != nil {
		fmt.Fprintf(os.Stderr, "error executing template: %v\n", err)
		os.Exit(1)
	}
}
