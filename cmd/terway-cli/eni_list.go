package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"

	aliClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
)

var (
	listStatus  string
	listNodeID  string
	listVpcID   string
	listShowAll bool
)

// eniListCmd represents the list command for ENI
var eniListCmd = &cobra.Command{
	Use:   "list",
	Short: "List ENI network interfaces",
	Long:  `Query and display ENI network interfaces. By default, only abnormal interfaces are shown.`,
	RunE:  runEniList,
}

func init() {
	eniListCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status (optional)")
	eniListCmd.Flags().StringVar(&listNodeID, "node-id", "", "Filter by node ID (optional)")
	eniListCmd.Flags().StringVar(&listVpcID, "vpc-id", "", "Filter by VPC ID (optional)")
	eniListCmd.Flags().BoolVar(&listShowAll, "show-all", false, "Show all network interfaces (default: only show abnormal)")

	eniCmd.AddCommand(eniListCmd)
}

func runEniList(cmd *cobra.Command, args []string) error {
	if err := validateLinkType(); err != nil {
		return err
	}

	ctx := context.Background()

	// Build query options
	opts := &aliClient.DescribeNetworkInterfaceOptions{}
	if listNodeID != "" {
		opts.InstanceID = &listNodeID
	}
	if listVpcID != "" {
		opts.VPCID = &listVpcID
	}
	if listStatus != "" {
		opts.Status = &listStatus
	}

	var enis []*aliClient.NetworkInterface
	var err error

	if linkType == LinkTypeECS {
		// Use ECS client for regular ENIs
		ecsClient, clientErr := getECSClient(regionID)
		if clientErr != nil {
			return clientErr
		}
		enis, err = ecsClient.DescribeNetworkInterface2(ctx, opts)
		if err != nil {
			return fmt.Errorf("failed to describe ECS network interfaces: %w", err)
		}
	} else {
		// Use EFLO client for LENI/HDENI (default)
		efloClient, clientErr := getEFLOClient(regionID)
		if clientErr != nil {
			return clientErr
		}
		// Always use raw status for accurate filtering for EFLO
		rawStatus := true
		opts.RawStatus = &rawStatus
		enis, err = efloClient.DescribeLeniNetworkInterface(ctx, opts)
		if err != nil {
			return fmt.Errorf("failed to describe EFLO network interfaces: %w", err)
		}
	}

	// Filter to only abnormal statuses if not showing all
	var filteredENIs []*aliClient.NetworkInterface
	if listShowAll || listStatus != "" {
		filteredENIs = enis
	} else {
		for _, eni := range enis {
			if isAbnormalStatusForLinkType(eni.Status, linkType) {
				filteredENIs = append(filteredENIs, eni)
			}
		}
	}

	if len(filteredENIs) == 0 {
		if listShowAll {
			if linkType == LinkTypeECS {
				pterm.Info.Println("No ECS ENI network interfaces found")
			} else {
				pterm.Info.Println("No EFLO (LENI/HDENI) network interfaces found")
			}
		} else {
			if linkType == LinkTypeECS {
				pterm.Info.Println("No abnormal ECS ENI network interfaces found")
			} else {
				pterm.Info.Println("No abnormal EFLO (LENI/HDENI) network interfaces found")
			}
		}
		return nil
	}

	// Display results in a table
	tableData := pterm.TableData{
		{"NetworkInterfaceId", "NodeId", "Status", "Type", "Ip", "VSwitchId", "Tags"},
	}

	for _, eni := range filteredENIs {
		nodeID := eni.InstanceID
		if nodeID == "" {
			nodeID = "-"
		}
		ip := eni.PrivateIPAddress
		if ip == "" {
			ip = "-"
		}
		vSwitchID := eni.VSwitchID
		if vSwitchID == "" {
			vSwitchID = "-"
		}
		eniType := eni.Type
		if eniType == "" {
			eniType = "-"
		}

		// Format tags as key=value pairs separated by commas
		tagsStr := "-"
		if len(eni.Tags) > 0 {
			tagPairs := make([]string, 0, len(eni.Tags))
			for _, tag := range eni.Tags {
				tagPairs = append(tagPairs, fmt.Sprintf("%s=%s", tag.TagKey, tag.TagValue))
			}
			tagsStr = strings.Join(tagPairs, ",")
		}

		// Colorize abnormal statuses
		status := eni.Status
		if isAbnormalStatusForLinkType(status, linkType) {
			status = pterm.Red(status)
		} else {
			// Green for normal/healthy status
			if linkType == LinkTypeECS && status == aliClient.ENIStatusInUse {
				status = pterm.Green(status)
			} else if linkType == LinkTypeEFLO && status == aliClient.LENIStatusAvailable {
				status = pterm.Green(status)
			}
		}

		tableData = append(tableData, []string{
			eni.NetworkInterfaceID,
			nodeID,
			status,
			eniType,
			ip,
			vSwitchID,
			tagsStr,
		})
	}

	var title string
	if linkType == LinkTypeECS {
		title = fmt.Sprintf("Found %d ECS ENI network interface(s)", len(filteredENIs))
		if !listShowAll && listStatus == "" {
			title = fmt.Sprintf("Found %d abnormal ECS ENI network interface(s)", len(filteredENIs))
		}
	} else {
		title = fmt.Sprintf("Found %d EFLO (LENI/HDENI) network interface(s)", len(filteredENIs))
		if !listShowAll && listStatus == "" {
			title = fmt.Sprintf("Found %d abnormal EFLO (LENI/HDENI) network interface(s)", len(filteredENIs))
		}
	}

	pterm.DefaultHeader.WithFullWidth().WithBackgroundStyle(pterm.NewStyle(pterm.BgDarkGray)).Println(title)

	err = pterm.DefaultTable.
		WithHasHeader(true).
		WithData(tableData).
		Render()
	if err != nil {
		return fmt.Errorf("failed to render table: %w", err)
	}

	return nil
}
