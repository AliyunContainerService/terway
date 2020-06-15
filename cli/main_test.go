package main

import (
	"fmt"
	"github.com/AliyunContainerService/terway/rpc"
	"testing"
)

func TestPrintTree(t *testing.T) {
	kvs := []*rpc.MapKeyValueEntry{
		{Key: "name", Value: "eniip"},
		{Key: "max", Value: "20"},
		{Key: "count", Value: "0"},
		{Key: "123/456/value1", Value: "1"},
		{Key: "123/456/value2", Value: "2"},
		{Key: "123/444/value1", Value: "1"},
	}

	printMapAsTree(kvs)
}

func TestPrintColors(t *testing.T) {
	for _, v := range outputColors {
		fmt.Print(v)
		fmt.Println("Test Test Test Test")
	}
}
