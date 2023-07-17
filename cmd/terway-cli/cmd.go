package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/AliyunContainerService/terway/pkg/aliyun/metadata"
	"github.com/AliyunContainerService/terway/rpc"

	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	"github.com/spf13/cobra"
)

func runList(cmd *cobra.Command, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("too many arguments")
	}

	var result []string
	if len(args) == 0 { // list types
		placeholder := &rpc.Placeholder{}
		types, err := client.GetResourceTypes(ctx, placeholder)
		if err != nil {
			return err
		}

		result = types.TypeNames
	} else {
		// list resources
		resource := args[0]
		request := &rpc.ResourceTypeRequest{Name: resource}
		resources, err := client.GetResources(ctx, request)
		if err != nil {
			return err
		}

		result = resources.ResourceNames
	}

	var items []pterm.BulletListItem
	for _, r := range result {
		items = append(items, pterm.BulletListItem{
			Level: 0,
			Text:  r,
		})
	}

	err := pterm.DefaultBulletList.
		WithTextStyle(&pterm.ThemeDefault.BarLabelStyle).
		WithBullet("*").
		WithItems(items).
		Render()

	return err
}

func runShow(cmd *cobra.Command, args []string) error {
	// todo: 去锁
	if len(args) >= 2 {
		return fmt.Errorf("too many arguments")
	}

	if len(args) == 0 {
		return fmt.Errorf("no arguments")
	}

	typ, name := args[0], ""
	if len(args) == 1 { // only type, select the first resource returned
		n, err := getFirstNameWithType(typ)
		if err != nil {
			return err
		}
		name = n
	} else {
		name = args[1]
	}

	request := &rpc.ResourceTypeNameRequest{
		Type: typ,
		Name: name,
	}

	cfg, err := client.GetResourceConfig(ctx, request)
	if err != nil {
		return err
	}

	trace, err := client.GetResourceTrace(ctx, request)
	if err != nil {
		return err
	}

	final := append(cfg.Config, trace.Trace...)
	err = printPTermTree(final)

	return err
}

const (
	mappingTableHeaderStatus            = "Status"
	mappingTableHeaderPodName           = "Pod Name"
	mappingTableHeaderResourceID        = "Res ID"
	mappingTableHeaderFactoryResourceID = "Factory Res ID"

	mappingStringErrorExists = "error exists in mapping"
)

var (
	mappingStatus = map[rpc.ResourceMappingType]string{
		rpc.ResourceMappingType_MappingTypeNormal: "Normal",
		rpc.ResourceMappingType_MappingTypeIdle:   "Idle",
		rpc.ResourceMappingType_MappingTypeError:  "ERROR",
	}
)

func runMapping(cmd *cobra.Command, args []string) error {
	placeholder := &rpc.Placeholder{}
	result, err := client.GetResourceMapping(ctx, placeholder)
	if err != nil {
		return err
	}

	tableData := pterm.TableData{
		{
			mappingTableHeaderStatus,
			mappingTableHeaderPodName,
			mappingTableHeaderResourceID,
			mappingTableHeaderFactoryResourceID,
		},
	}

	for _, v := range result.Info {
		clr := pterm.FgDefault
		switch v.Type {
		case rpc.ResourceMappingType_MappingTypeNormal:
			// Idle
			if v.PodName == "" {
				v.Type = rpc.ResourceMappingType_MappingTypeIdle
				clr = pterm.FgLightCyan
			}
		case rpc.ResourceMappingType_MappingTypeError:
			clr = pterm.FgLightRed
			err = fmt.Errorf(mappingStringErrorExists)
		}

		row := []string{
			clr.Sprint(mappingStatus[v.Type]),
			clr.Sprint(v.PodName),
			clr.Sprint(v.ResourceName),
			clr.Sprint(v.FactoryResourceName),
		}
		tableData = append(tableData, row)
	}

	if err := pterm.DefaultTable.WithHasHeader().WithData(tableData).Render(); err != nil {
		return err
	}

	return err
}

func runExecute(cmd *cobra.Command, args []string) error {
	// <type> <resource> <command> [args...]
	if len(args) < 3 {
		return fmt.Errorf("too few arguments")
	}

	typ, name, command := args[0], args[1], args[2]
	args = args[3:]

	request := &rpc.ResourceExecuteRequest{
		Type:    typ,
		Name:    name,
		Command: command,
		Args:    args,
	}

	stream, err := client.ResourceExecute(ctx, request)
	if err != nil {
		return err
	}

	var message *rpc.ResourceExecuteReply

	for {
		message, err = stream.Recv()
		if err != nil {
			break
		}

		fmt.Print(message.Message) // print message
	}

	if err == io.EOF {
		return nil
	}

	return err
}

const (
	metadataErrorStringIPV6 = "can't get ipv6 info for eni %s, skipping. (%s)"

	metadataLevelVSwitch       = 0
	metadataLevelENI           = 1
	metadataLevelAttribute     = 2
	metadataLevelAttributeItem = 3
)

func runMetadata(cmd *cobra.Command, args []string) error {
	leveledList := pterm.LeveledList{}

	vsw, err := metadata.GetLocalVswitch()
	if err != nil {
		return err
	}

	leveledList = append(leveledList, pterm.LeveledListItem{
		Level: metadataLevelVSwitch,
		Text:  printKV("vswitch", vsw),
	})

	enis, err := metadata.GetENIsMAC()
	if err != nil {
		return err
	}

	primaryENI, err := metadata.GetPrimaryENIMAC()
	if err != nil {
		return err
	}

	// make primary to the first
	sort.Slice(enis, func(i, j int) bool {
		return enis[i] == primaryENI
	})

	for _, eni := range enis {
		leveledList = append(leveledList, pterm.LeveledListItem{
			Level: metadataLevelENI,
			Text:  printKV("eni", eni),
		}, pterm.LeveledListItem{
			Level: metadataLevelAttribute,
			Text:  printKV("primary", fmt.Sprintf("%t", primaryENI == eni)),
		})

		// network interface
		nif, err := getInterfaceByMAC(eni)
		if err != nil && err.Error() != "not found" {
			return err
		}

		ifname := ""
		if err != nil {
			ifname = pterm.Red("!!!NOT FOUND")
		} else {
			ifname = nif.Name
		}

		leveledList = append(leveledList, pterm.LeveledListItem{
			Level: metadataLevelAttribute,
			Text:  printKV("interface", ifname),
		})

		primaryIP, err := metadata.GetENIPrimaryIP(eni)
		if err != nil {
			return err
		}

		leveledList = append(leveledList, pterm.LeveledListItem{
			Level: metadataLevelAttribute,
			Text:  printKV("primary_ipv4", primaryIP.String()),
		})

		// ipv4
		ipv4s, err := metadata.GetENIPrivateIPs(eni)
		if err != nil {
			return err
		}

		// when len(ipv4) == 1, only primary ipv4 exists
		if len(ipv4s) > 1 {
			leveledList = append(leveledList, pterm.LeveledListItem{
				Level: metadataLevelAttribute,
				Text:  "secondary_ipv4s",
			})

			sort.Slice(ipv4s, func(i, j int) bool {
				return ipv4s[i].String() < ipv4s[j].String()
			})

			for _, ip := range ipv4s {
				if ip.Equal(primaryIP) {
					continue
				}

				leveledList = append(leveledList, pterm.LeveledListItem{
					Level: metadataLevelAttributeItem,
					Text:  pterm.ThemeDefault.WarningMessageStyle.Sprint(ip.String()),
				})
			}
		}

		// ipv6
		ipv6s, err := metadata.GetENIPrivateIPv6IPs(eni)
		if err != nil {
			pterm.Error.Printf(metadataErrorStringIPV6, eni, err)
			continue
		}

		if len(ipv6s) != 0 {
			leveledList = append(leveledList, pterm.LeveledListItem{
				Level: metadataLevelAttribute,
				Text:  "ipv6s",
			})

			sort.Slice(ipv6s, func(i, j int) bool {
				return strings.Compare(ipv6s[i].String(), ipv6s[j].String()) < 0
			})

			for _, ip := range ipv6s {
				leveledList = append(leveledList, pterm.LeveledListItem{
					Level: metadataLevelAttributeItem,
					Text:  pterm.ThemeDefault.WarningMessageStyle.Sprint(ip.String()),
				})
			}
		}
	}

	tree := putils.TreeFromLeveledList(leveledList)
	return pterm.DefaultTree.
		WithTextStyle(&pterm.ThemeDefault.BarLabelStyle).
		WithRoot(tree).
		Render()
}

func printKV(key, value string) string {
	return fmt.Sprintf("%s: %s", key, pterm.ThemeDefault.WarningMessageStyle.Sprint(value))
}

// getFirstNameWithType finds the first resource in the given type
func getFirstNameWithType(typ string) (string, error) {
	request := &rpc.ResourceTypeRequest{Name: typ}
	resource, err := client.GetResources(ctx, request)
	if err != nil {
		return "", err
	}

	if len(resource.ResourceNames) == 0 {
		return "", fmt.Errorf("no resource in the specified type %s", typ)
	}

	return resource.ResourceNames[0], nil
}
