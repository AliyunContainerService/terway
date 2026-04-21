package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/trace/noop"

	aliClient "github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
)

var (
	regionID string
	linkType string
)

const (
	LinkTypeEFLO = "eflo"
	LinkTypeECS  = "ecs"
)

// eniCmd is the root command for ENI management (supports both EFLO/LENI/HDENI and ECS ENIs)
var eniCmd = &cobra.Command{
	Use:   "eni",
	Short: "Manage ENI network interfaces (EFLO/LENI/HDENI and ECS)",
	Long:  `Query and clean up ENI network interfaces. Supports both EFLO (LENI/HDENI) and regular ECS ENIs.`,
}

func init() {
	eniCmd.PersistentFlags().StringVar(&regionID, "region", os.Getenv("REGION_ID"), "Alibaba Cloud region ID (can also be set via REGION_ID env)")
	eniCmd.PersistentFlags().StringVar(&linkType, "link-type", LinkTypeECS, "Link type: 'eflo' for LENI/HDENI, 'ecs' for regular ECS ENIs")
}

// validateLinkType validates the link type flag
func validateLinkType() error {
	if linkType != LinkTypeEFLO && linkType != LinkTypeECS {
		return fmt.Errorf("invalid link-type '%s', must be either '%s' or '%s'", linkType, LinkTypeEFLO, LinkTypeECS)
	}
	return nil
}

// isValidEFLOENIID checks if an ENI ID has valid EFLO prefix (hdeni- or leni-)
func isValidEFLOENIID(eniID string) bool {
	return strings.HasPrefix(eniID, "hdeni-") || strings.HasPrefix(eniID, "leni-")
}

// validateENIIDsForLinkType validates ENI IDs based on link type
func validateENIIDsForLinkType(eniIDs []string, lType string) error {
	if lType != LinkTypeEFLO {
		return nil
	}
	for _, id := range eniIDs {
		if !isValidEFLOENIID(id) {
			return fmt.Errorf("invalid EFLO ENI ID '%s': must have 'hdeni-' or 'leni-' prefix", id)
		}
	}
	return nil
}

// getCredentialProvider creates a credential provider chain for Alibaba Cloud
func getCredentialProvider() provider.CredentialsProvider {
	return provider.NewChainProvider(
		provider.NewAccessKeyProvider(os.Getenv("ALICLOUD_ACCESS_KEY"), os.Getenv("ALICLOUD_SECRET_KEY")),
		provider.NewEncryptedFileProvider(provider.EncryptedFileProviderOptions{
			FilePath:      os.Getenv("ALICLOUD_CREDENTIALS_FILE"),
			RefreshPeriod: 30 * time.Minute,
		}),
		provider.NewECSMetadataProvider(provider.ECSMetadataProviderOptions{}),
	)
}

// getEFLOClient initializes the EFLO client with the given region
func getEFLOClient(region string) (aliClient.EFLO, error) {
	if region == "" {
		return nil, fmt.Errorf("region is required, please specify --region or set REGION_ID environment variable")
	}

	credProvider := getCredentialProvider()

	clientMgr, err := credential.InitializeClientMgr(region, credProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize client manager: %w", err)
	}

	// Use default rate limiter configuration
	rateLimiter := aliClient.NewRateLimiter(aliClient.LimitConfig{})

	// Use noop tracer for CLI
	tracer := noop.NewTracerProvider().Tracer("terway-cli")

	return aliClient.NewEFLOService(clientMgr, rateLimiter, tracer), nil
}

// getECSClient initializes the ECS client with the given region
func getECSClient(region string) (aliClient.ECS, error) {
	if region == "" {
		return nil, fmt.Errorf("region is required, please specify --region or set REGION_ID environment variable")
	}

	credProvider := getCredentialProvider()

	clientMgr, err := credential.InitializeClientMgr(region, credProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize client manager: %w", err)
	}

	// Use default rate limiter configuration
	rateLimiter := aliClient.NewRateLimiter(aliClient.LimitConfig{})

	// Use noop tracer for CLI
	tracer := noop.NewTracerProvider().Tracer("terway-cli")

	return aliClient.NewECSService(clientMgr, rateLimiter, tracer), nil
}

// getEFLOControlClient initializes the EFLO Control client with the given region
func getEFLOControlClient(region string) (aliClient.EFLOControl, error) {
	if region == "" {
		return nil, fmt.Errorf("region is required, please specify --region or set REGION_ID environment variable")
	}

	credProvider := getCredentialProvider()

	clientMgr, err := credential.InitializeClientMgr(region, credProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize client manager: %w", err)
	}

	rateLimiter := aliClient.NewRateLimiter(aliClient.LimitConfig{})
	tracer := noop.NewTracerProvider().Tracer("terway-cli")

	return aliClient.NewEFLOControlService(clientMgr, rateLimiter, tracer), nil
}

// isAbnormalStatus checks if an ENI status indicates an abnormal state
// For ENI (ECS): Available status means not attached, which is abnormal
// For ENO (LENI): Unattached status means not attached, which is abnormal
func isAbnormalStatus(status string) bool {
	abnormalStatuses := []string{
		aliClient.LENIStatusCreateFailed,
		aliClient.LENIStatusAttachFailed,
		aliClient.LENIStatusDeleteFailed,
		aliClient.LENIStatusDetachFailed,
		aliClient.LENIStatusUnattached,
	}
	for _, s := range abnormalStatuses {
		if status == s {
			return true
		}
	}
	return false
}

// isAbnormalStatusForLinkType checks if an ENI status is abnormal based on link type
func isAbnormalStatusForLinkType(status, lType string) bool {
	if lType == LinkTypeECS {
		// For ECS ENIs, "Available" means not attached to any instance
		if status == aliClient.ENIStatusAvailable {
			return true
		}
		// Any status other than InUse is considered abnormal for ENI
		if status != aliClient.ENIStatusInUse {
			return true
		}
		return false
	}
	// For EFLO (LENI/HDENI), use the standard abnormal status check
	return isAbnormalStatus(status)
}

// parseENIIDs parses a comma-separated list of ENI IDs
func parseENIIDs(ids string) []string {
	if ids == "" {
		return nil
	}
	parts := strings.Split(ids, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
