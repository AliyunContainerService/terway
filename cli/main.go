package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/AliyunContainerService/terway/rpc"
	"google.golang.org/grpc"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

const defaultSocketPath = "/var/run/eni/eni.socket"
const helpString = `Terway Tracing CLI
    subcommands:
        list [type] - show types/resources list
        config <type> [resource_name] - get config of the resource, get the first if name not specified
        trace <type> [resource_name] - get trace of the resource, get the first if name not specified
        show  <type> [resource_name] - get both config & trace of the resource
        exec <type> <resource> <command> args... - send command to the given resource
		mapping - get terway resource mapping
`

type subcommandHandler func(ctx context.Context, c rpc.TerwayTracingClient, args []string) error

var subcommands = map[string]subcommandHandler{
	"list":    list,
	"config":  config,
	"trace":   trace,
	"show":    show,
	"exec":    exec,
	"mapping": mapping,
}

var outputColors = map[rpc.ResourceMappingType]string{
	rpc.ResourceMappingType_MappingTypeNormal: "\033[0m",
	rpc.ResourceMappingType_MappingTypeIdle:   "\033[0;34m",
	rpc.ResourceMappingType_MappingTypeError:  "\033[1;31m",
}

const (
	TableHeaderPodName           = "Pod Name"
	TableHeaderResourceID        = "Res ID"
	TableHeaderFactoryResourceID = "Factory Res ID"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Print(helpString)
		_, _ = fmt.Fprintf(os.Stderr, "parameter error.\n")
		os.Exit(1)
	}

	// initialize gRPC
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	grpcConn, err := grpc.DialContext(ctx, defaultSocketPath, grpc.WithInsecure(), grpc.WithContextDialer(
		func(ctx context.Context, s string) (net.Conn, error) {
			unixAddr, err := net.ResolveUnixAddr("unix", defaultSocketPath)
			if err != nil {
				return nil, nil
			}
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", unixAddr.String())
		}))

	if err != nil {
		cancel()
		_, _ = fmt.Fprintf(os.Stderr, "error while dialing to daemon: %s", err.Error())
		os.Exit(1)
	}

	defer grpcConn.Close()
	defer cancel()
	c := rpc.NewTerwayTracingClient(grpcConn)

	args := os.Args[1:]
	handler, ok := subcommands[args[0]]
	if ok {
		err = handler(ctx, c, args[1:])
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
	}

	if err != nil {
		cancel()
		_ = grpcConn.Close()
		_, _ = fmt.Fprintf(os.Stderr, "error: %s\n", err.Error())
		os.Exit(1)
	}
}

func list(ctx context.Context, c rpc.TerwayTracingClient, args []string) error {
	if len(args) > 1 {
		return errors.New("too many arguments")
	}

	if len(args) == 0 { // list types
		placeholder := &rpc.Placeholder{}
		types, err := c.GetResourceTypes(ctx, placeholder)
		if err != nil {
			return err
		}
		fmt.Printf("%v\n", types.TypeNames)
		return nil
	}

	// list resources
	resource := args[0]
	request := &rpc.ResourceTypeRequest{Name: resource}
	resources, err := c.GetResources(ctx, request)
	if err != nil {
		return err
	}

	fmt.Printf("%v\n", resources.ResourceNames)
	return nil
}

func config(ctx context.Context, c rpc.TerwayTracingClient, args []string) error {
	if len(args) > 2 {
		return errors.New("too many arguments")
	}

	typ, name := args[0], ""
	if len(args) == 1 { // only type, select the first resource returned
		request := &rpc.ResourceTypeRequest{Name: typ}
		resource, err := c.GetResources(ctx, request)
		if err != nil {
			return err
		}

		if len(resource.ResourceNames) == 0 {
			return errors.New("no resource in the specified type")
		}
		name = resource.ResourceNames[0]
	} else {
		name = args[1]
	}

	request := &rpc.ResourceTypeNameRequest{
		Type: typ,
		Name: name,
	}

	cfg, err := c.GetResourceConfig(ctx, request)
	if err != nil {
		return err
	}

	printMapAsTree(cfg.Config)
	return nil
}

func trace(ctx context.Context, c rpc.TerwayTracingClient, args []string) error {
	if len(args) > 2 {
		return errors.New("too many arguments")
	}

	typ, name := args[0], ""
	if len(args) == 1 { // only type, select the first resource returned
		n, err := getFirstNameWithType(c, ctx, typ)
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

	trace, err := c.GetResourceTrace(ctx, request)
	if err != nil {
		return err
	}

	printMapAsTree(trace.Trace)
	return nil
}

func show(ctx context.Context, c rpc.TerwayTracingClient, args []string) error {
	if len(args) >= 2 {
		return errors.New("too many arguments")
	}

	typ, name := args[0], ""
	if len(args) == 1 { // only type, select the first resource returned
		n, err := getFirstNameWithType(c, ctx, typ)
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

	cfg, err := c.GetResourceConfig(ctx, request)
	if err != nil {
		return err
	}

	trace, err := c.GetResourceTrace(ctx, request)
	if err != nil {
		return err
	}

	final := append(cfg.Config, trace.Trace...)
	printMapAsTree(final)

	return nil
}

func exec(ctx context.Context, c rpc.TerwayTracingClient, args []string) error {
	// <type> <resource> <command> [args...]
	if len(args) < 3 {
		return errors.New("too few arguments")
	}

	typ, name, command := args[0], args[1], args[2]
	args = args[3:]

	request := &rpc.ResourceExecuteRequest{
		Type:    typ,
		Name:    name,
		Command: command,
		Args:    args,
	}

	stream, err := c.ResourceExecute(ctx, request)
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

func mapping(ctx context.Context, c rpc.TerwayTracingClient, _ []string) error {
	placeholder := &rpc.Placeholder{}
	result, err := c.GetResourceMapping(ctx, placeholder)
	if err != nil {
		return err
	}

	cPod := len(TableHeaderPodName)
	cResource := len(TableHeaderResourceID)
	cFactory := len(TableHeaderFactoryResourceID)

	// calculate the appropriate column length
	for _, v := range result.Info {
		if len(v.PodName) == 0 {
			v.PodName = "X"
		}
		if cPod < len(v.PodName) {
			cPod = len(v.PodName)
		}

		if len(v.ResourceName) == 0 {
			v.ResourceName = "X"
		}
		if cResource < len(v.ResourceName) {
			cResource = len(v.ResourceName)
		}

		if len(v.FactoryResourceName) == 0 {
			v.FactoryResourceName = "X"
		}
		if cFactory < len(v.FactoryResourceName) {
			cFactory = len(v.FactoryResourceName)
		}
	}

	fmtString := fmt.Sprintf("%%-%ds%%-%ds%%-%ds\n", cPod+3, cResource+3, cFactory+3)
	// print table header
	fmt.Printf(fmtString, TableHeaderPodName, TableHeaderResourceID, TableHeaderFactoryResourceID)
	for _, v := range result.Info {
		fmt.Print(outputColors[v.Type]) // print color
		fmt.Printf(fmtString, v.PodName, v.ResourceName, v.FactoryResourceName)

		if err == nil && v.Type == rpc.ResourceMappingType_MappingTypeError {
			err = errors.New("error exists in mapping")
		}
	}

	fmt.Print(outputColors[rpc.ResourceMappingType_MappingTypeNormal])

	return err
}

// getFirstNameWithType finds the first resource in the given type
func getFirstNameWithType(c rpc.TerwayTracingClient, ctx context.Context, typ string) (string, error) {
	request := &rpc.ResourceTypeRequest{Name: typ}
	resource, err := c.GetResources(ctx, request)
	if err != nil {
		return "", err
	}

	if len(resource.ResourceNames) == 0 {
		return "", errors.New("no resource in the specified type")
	}

	return resource.ResourceNames[0], nil
}

//func printMap(m []*rpc.MapKeyValueEntry) {
//	if len(m) == 0 {
//		fmt.Printf("no record\n")
//		return
//	}
//
//	// get the longest length in k
//	length := 0
//	for _, v := range m {
//		if len(v.Key) > length {
//			length = len(v.Key)
//		}
//	}
//
//	fmtString := fmt.Sprintf("%%-%ds%%s\n", length+3)
//	for _, v := range m {
//		fmt.Printf(fmtString, v.Key+":", v.Value)
//	}
//}

func printMapAsTree(m []*rpc.MapKeyValueEntry) {
	// build a tree
	t := &Tree{}
	for _, v := range m {
		t.AddLeaf(strings.Split(v.Key, "/"), v.Value)
	}

	// print the tree
	for _, v := range t.Leaves {
		v.Print(os.Stdout, "    ", "")
	}
}
