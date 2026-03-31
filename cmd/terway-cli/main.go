package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	cliflag "k8s.io/component-base/cli/flag"

	_ "github.com/AliyunContainerService/terway/pkg/feature"
	"github.com/AliyunContainerService/terway/rpc"
)

const (
	defaultSocketPath = "/var/run/eni/eni.socket"

	connTimeout = time.Second * 30
)

var (
	grpcConn      *grpc.ClientConn
	ctx           context.Context
	contextCancel context.CancelFunc
	client        rpc.TerwayTracingClient
)

var (
	rootCmd = &cobra.Command{
		Use:   "terway-cli",
		Short: "terway-cil is a command tool for diagnosing terway & network internal status.",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return utilfeature.DefaultMutableFeatureGate.SetFromMap(featureGates)
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			if contextCancel != nil {
				contextCancel()
			}
			if grpcConn != nil {
				_ = grpcConn.Close()
			}
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	listCmd = &cobra.Command{
		Use:     "list [type]",
		Short:   "show types/resources list.",
		PreRunE: grpcPreRunE,
		RunE:    runList,
	}

	showCmd = &cobra.Command{
		Use:     "show <type> [resource_name]",
		Short:   "show config",
		Long:    "show config and trace info of the resource, get the first if name not specified.",
		PreRunE: grpcPreRunE,
		RunE:    runShow,
	}

	mappingCmd = &cobra.Command{
		Use:     "mapping",
		Short:   "get terway resource mappings.",
		PreRunE: grpcPreRunE,
		RunE:    runMapping,
	}

	executeCmd = &cobra.Command{
		Use:     "execute <type> <resource> <command> [args...]",
		Short:   "send command to the given resource.",
		PreRunE: grpcPreRunE,
		RunE:    runExecute,
	}

	metadataCmd = &cobra.Command{
		Use:   "metadata",
		Short: "Show metadata of this node",
		RunE:  runMetadata,
	}
)

func grpcPreRunE(_ *cobra.Command, _ []string) error {
	ctx, contextCancel = context.WithTimeout(context.Background(), connTimeout)
	conn, err := grpc.NewClient("passthrough:"+defaultSocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(
		func(ctx context.Context, s string) (net.Conn, error) {
			unixAddr, err := net.ResolveUnixAddr("unix", defaultSocketPath)
			if err != nil {
				return nil, fmt.Errorf("error while resolve unix addr:%w", err)
			}
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", unixAddr.String())
		}))
	if err != nil {
		contextCancel()
		return err
	}

	grpcConn = conn
	client = rpc.NewTerwayTracingClient(conn)
	return nil
}

func init() {
	rootCmd.PersistentFlags().Var(cliflag.NewMapStringBool(&featureGates), "feature-gates", "A set of key=value pairs that describe feature gates for alpha/experimental features. "+
		"Options are:\n"+strings.Join(utilfeature.DefaultFeatureGate.KnownFeatures(), "\n"))

	rootCmd.AddCommand(listCmd, showCmd, mappingCmd, executeCmd, metadataCmd, cniCmd, nodeconfigCmd, policyCmd, eniCmd, migrateCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
