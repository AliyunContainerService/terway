package main

import (
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
