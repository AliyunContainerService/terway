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
        execute <type> <resource> <command> args... - send command to the given resource
`

func main() {
	/*
		subcommands:
		list [type] - show types/resources list
		config <type> [resource] - get config of resource
		trace <type> [resource] - get trace of resource
		exec <type> <resource> <command> [args...]
	*/

	if len(os.Args) < 2 {
		fmt.Printf(helpString)
		_, _ = fmt.Fprintf(os.Stderr, "parameter error.\n")
		os.Exit(1)
	}

	// initialize grpc
	ctx, _ := context.WithTimeout(context.Background(), time.Second*10)
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
		_, _ = fmt.Fprintf(os.Stderr, "error while dialing to daemon: %s", err.Error())
		os.Exit(1)
	}

	defer grpcConn.Close()
	c := rpc.NewTerwayTracingClient(grpcConn)

	args := os.Args[1:]
	switch args[0] { // subcommands
	case "list":
		err = list(c, ctx, args[1:])
	case "config":
		err = config(c, ctx, args[1:])
	case "trace":
		err = trace(c, ctx, args[1:])
	case "show":
		err = show(c, ctx, args[1:])
	case "exec":
		err = exec(c, ctx, args[1:])
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", args[0])
	}

	if err != nil {
		_ = grpcConn.Close()
		_, _ = fmt.Fprintf(os.Stderr, "error: %s\n", err.Error())
		os.Exit(1)
	}

}

func list(c rpc.TerwayTracingClient, ctx context.Context, args []string) error {
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

func config(c rpc.TerwayTracingClient, ctx context.Context, args []string) error {
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

func trace(c rpc.TerwayTracingClient, ctx context.Context, args []string) error {
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

func show(c rpc.TerwayTracingClient, ctx context.Context, args []string) error {
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

func exec(c rpc.TerwayTracingClient, ctx context.Context, args []string) error {
	// <type> <resource> <commmand> [args...]
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

func printMap(m []*rpc.MapKeyValueEntry) {
	if len(m) == 0 {
		fmt.Printf("no record\n")
		return
	}

	// get the longest length in k
	length := 0
	for _, v := range m {
		if len(v.Key) > length {
			length = len(v.Key)
		}
	}

	fmtString := fmt.Sprintf("%%-%ds%%s\n", length+3)
	for _, v := range m {
		fmt.Printf(fmtString, v.Key+":", v.Value)
	}
}

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
