package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	aliClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
)

var (
	deleteIDs    string
	deleteBatch  bool
	deleteStatus string
	deleteNodeID string
	deleteYes    bool
	deleteLimit  int
)

// eniDeleteCmd represents the delete command for ENI
var eniDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete ENI network interfaces",
	Long: `Delete specified ENI network interfaces.

Examples:
  # Delete specific EFLO ENIs by ID
  terway-cli eni delete --ids leni-xxx,leni-yyy --region cn-hangzhou --yes

  # Delete specific ECS ENIs by ID
  terway-cli eni delete --link-type ecs --ids eni-xxx,eni-yyy --region cn-hangzhou --yes

  # Batch delete ECS ENIs with specific status on a node
  terway-cli eni delete --link-type ecs --batch --status Available --node-id node-xxx --region cn-hangzhou --yes`,
	RunE: runEniDelete,
}

func init() {
	eniDeleteCmd.Flags().StringVar(&deleteIDs, "ids", "", "Comma-separated list of ENI IDs to delete (mutually exclusive with --batch)")
	eniDeleteCmd.Flags().BoolVar(&deleteBatch, "batch", false, "Batch delete mode - delete all matching abnormal ENIs")
	eniDeleteCmd.Flags().StringVar(&deleteStatus, "status", "", "Filter by status (only used with --batch)")
	eniDeleteCmd.Flags().StringVar(&deleteNodeID, "node-id", "", "Filter by node ID (only used with --batch)")
	eniDeleteCmd.Flags().BoolVar(&deleteYes, "yes", false, "Skip confirmation prompt")
	eniDeleteCmd.Flags().IntVar(&deleteLimit, "limit", 50, "Maximum number of ENIs to delete per batch (max 50)")

	eniCmd.AddCommand(eniDeleteCmd)
}

func runEniDelete(cmd *cobra.Command, args []string) error {
	// Validate arguments
	if deleteIDs == "" && !deleteBatch {
		return fmt.Errorf("either --ids or --batch must be specified")
	}
	if deleteIDs != "" && deleteBatch {
		return fmt.Errorf("--ids and --batch are mutually exclusive")
	}
	// In batch mode, must specify either --status or --node-id as filter
	if deleteBatch && deleteStatus == "" && deleteNodeID == "" {
		return fmt.Errorf("in batch mode, either --status or --node-id must be specified as a filter")
	}
	if deleteLimit <= 0 || deleteLimit > 50 {
		return fmt.Errorf("--limit must be between 1 and 50")
	}
	if err := validateLinkType(); err != nil {
		return err
	}

	ctx := context.Background()

	// Get the list of ENI IDs to delete
	var eniIDs []string
	var err error
	if deleteIDs != "" {
		eniIDs = parseENIIDs(deleteIDs)
		if len(eniIDs) == 0 {
			return fmt.Errorf("no valid ENI IDs provided")
		}
		// Validate ENI IDs for EFLO mode
		if err := validateENIIDsForLinkType(eniIDs, linkType); err != nil {
			return err
		}
	} else {
		// Batch mode: query ENIs matching criteria
		eniIDs, err = queryENIsForBatchDelete(ctx)
		if err != nil {
			return err
		}
		if len(eniIDs) == 0 {
			if linkType == LinkTypeECS {
				pterm.Info.Println("No ECS ENIs matching the criteria found for deletion")
			} else {
				pterm.Info.Println("No EFLO (LENI/HDENI) ENIs matching the criteria found for deletion")
			}
			return nil
		}
		// Apply limit to the number of ENIs to delete
		if len(eniIDs) > deleteLimit {
			eniIDs = eniIDs[:deleteLimit]
		}
	}

	// Show the ENIs to be deleted
	err = showDeleteConfirmation(eniIDs)
	if err != nil {
		return err
	}

	// Confirm deletion unless --yes is specified
	if !deleteYes {
		confirmed, err := pterm.DefaultInteractiveConfirm.WithDefaultText("Do you want to proceed with deletion?").Show()
		if err != nil {
			return fmt.Errorf("failed to get confirmation: %w", err)
		}
		if !confirmed {
			pterm.Info.Println("Deletion cancelled")
			return nil
		}
	}

	// Delete ENIs in batches using appropriate client
	if linkType == LinkTypeECS {
		ecsClient, err := getECSClient(regionID)
		if err != nil {
			return err
		}
		return deleteENIsInBatches(ctx, &ecsDeleter{client: ecsClient}, eniIDs, deleteLimit)
	}
	efloClient, err := getEFLOClient(regionID)
	if err != nil {
		return err
	}
	return deleteENIsInBatches(ctx, &efloDeleter{client: efloClient}, eniIDs, deleteLimit)
}

func queryENIsForBatchDelete(ctx context.Context) ([]string, error) {
	opts := &aliClient.DescribeNetworkInterfaceOptions{}
	if deleteNodeID != "" {
		opts.InstanceID = &deleteNodeID
	}
	if deleteStatus != "" {
		opts.Status = &deleteStatus
	}

	var enis []*aliClient.NetworkInterface
	var err error

	if linkType == LinkTypeECS {
		// Use ECS client for regular ENIs
		ecsClient, clientErr := getECSClient(regionID)
		if clientErr != nil {
			return nil, clientErr
		}
		enis, err = ecsClient.DescribeNetworkInterface2(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to describe ECS network interfaces: %w", err)
		}
	} else {
		// Use EFLO client for LENI/HDENI
		efloClient, clientErr := getEFLOClient(regionID)
		if clientErr != nil {
			return nil, clientErr
		}
		// Always use raw status for accurate filtering for EFLO
		rawStatus := true
		opts.RawStatus = &rawStatus
		enis, err = efloClient.DescribeLeniNetworkInterface(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to describe EFLO network interfaces: %w", err)
		}
	}

	var eniIDs []string
	for _, eni := range enis {
		// If no specific status is provided, filter for abnormal ones
		if deleteStatus == "" {
			if isAbnormalStatusForLinkType(eni.Status, linkType) {
				eniIDs = append(eniIDs, eni.NetworkInterfaceID)
			}
		} else {
			eniIDs = append(eniIDs, eni.NetworkInterfaceID)
		}
	}

	return eniIDs, nil
}

func showDeleteConfirmation(eniIDs []string) error {
	title := fmt.Sprintf("About to delete %d ENI(s)", len(eniIDs))
	pterm.DefaultHeader.WithFullWidth().WithBackgroundStyle(pterm.NewStyle(pterm.BgDarkGray)).Println(title)

	// Show first 10 ENIs
	limit := 10
	if len(eniIDs) < limit {
		limit = len(eniIDs)
	}

	items := make([]pterm.BulletListItem, 0, limit+1)
	for i := 0; i < limit; i++ {
		items = append(items, pterm.BulletListItem{
			Level: 0,
			Text:  eniIDs[i],
		})
	}
	if len(eniIDs) > limit {
		items = append(items, pterm.BulletListItem{
			Level: 0,
			Text:  fmt.Sprintf("... and %d more", len(eniIDs)-limit),
		})
	}

	return pterm.DefaultBulletList.WithItems(items).Render()
}

// eniDeleter interface for deleting ENIs
type eniDeleter interface {
	deleteENI(ctx context.Context, eniID string) error
}

// efloDeleter wraps EFLO client to implement eniDeleter
type efloDeleter struct {
	client aliClient.EFLO
}

func (e *efloDeleter) deleteENI(ctx context.Context, eniID string) error {
	return e.client.DeleteElasticNetworkInterface(ctx, eniID)
}

// ecsDeleter wraps ECS client to implement eniDeleter
type ecsDeleter struct {
	client aliClient.ECS
}

func (e *ecsDeleter) deleteENI(ctx context.Context, eniID string) error {
	return e.client.DeleteNetworkInterface(ctx, eniID)
}

func deleteENIsInBatches(ctx context.Context, deleter eniDeleter, eniIDs []string, batchSize int) error {
	total := len(eniIDs)
	spinner, _ := pterm.DefaultSpinner.WithText(fmt.Sprintf("Deleting %d ENI(s)...", total)).Start()

	var successCount, failCount int
	var mu sync.Mutex

	// Process in batches
	for i := 0; i < total; i += batchSize {
		end := i + batchSize
		if end > total {
			end = total
		}
		batch := eniIDs[i:end]

		// Delete each ENI in the batch concurrently
		g := errgroup.Group{}
		for _, eniID := range batch {
			g.Go(func() error {
				err := deleter.deleteENI(ctx, eniID)
				mu.Lock()
				if err != nil {
					failCount++
					pterm.Error.Printf("Failed to delete ENI %s: %v\n", eniID, err)
				} else {
					successCount++
				}
				mu.Unlock()
				return nil // Continue processing other ENIs even if one fails
			})
		}
		_ = g.Wait()

		// Update spinner text
		spinner.UpdateText(fmt.Sprintf("Deleted %d/%d ENI(s)...", successCount+failCount, total))
	}

	spinner.Success()

	// Show final results
	pterm.Printf("\nResults:\n")
	pterm.Printf("  Success: %s\n", pterm.Green(successCount))
	if failCount > 0 {
		pterm.Printf("  Failed:  %s\n", pterm.Red(failCount))
		return fmt.Errorf("some ENIs failed to delete")
	}

	return nil
}
