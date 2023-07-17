package main

import (
	"fmt"
	"strings"

	"github.com/AliyunContainerService/terway/rpc"

	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
)

type tree struct {
	Key    string
	Value  string // value only exists in leaf
	IsLeaf bool
	Leaves []*tree
}

// add a leaf to the tree
func (t *tree) addLeaf(path []string, value string) {
	if len(path) == 0 { // noop
		return
	}

	if len(path) == 1 { // is leaf
		t.Leaves = append(t.Leaves, &tree{
			Key:    path[0],
			Value:  value,
			IsLeaf: true,
		})
		return
	}

	for _, v := range t.Leaves {
		if v.Key == path[0] && !v.IsLeaf {
			v.addLeaf(path[1:], value)
			return
		}
	}

	tree := &tree{
		Key:    path[0],
		IsLeaf: false,
		Leaves: nil,
	}
	t.Leaves = append(t.Leaves, tree)

	tree.addLeaf(path[1:], value)
}

func (t *tree) leveledList(list pterm.LeveledList, level int) pterm.LeveledList {
	if t.IsLeaf {
		return append(list, pterm.LeveledListItem{
			Level: level,
			Text:  fmt.Sprintf("%s: %s", t.Key, pterm.ThemeDefault.WarningMessageStyle.Sprint(t.Value)),
		})
	}

	list = append(list, pterm.LeveledListItem{
		Level: level,
		Text:  t.Key,
	})

	for _, v := range t.Leaves {
		list = v.leveledList(list, level+1)
	}

	return list
}

func printPTermTree(m []*rpc.MapKeyValueEntry) error {
	// build a tree
	t := &tree{}
	for _, v := range m {
		t.addLeaf(strings.Split(v.Key, "/"), v.Value)
	}

	list := pterm.LeveledList{}
	list = t.leveledList(list, 0)

	root := putils.TreeFromLeveledList(list)
	return pterm.DefaultTree.
		WithTextStyle(&pterm.ThemeDefault.BarLabelStyle).
		WithRoot(root).
		Render()
}
