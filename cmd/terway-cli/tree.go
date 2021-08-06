package main

import (
	"fmt"
	"io"
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

func (t *tree) print(w io.Writer, indent string, level string) {
	_, _ = fmt.Fprint(w, level)

	if t.IsLeaf {
		_, _ = fmt.Fprintf(w, "%s: %s\n", t.Key, t.Value)
		return
	}

	_, _ = fmt.Printf("%s:\n", t.Key)

	for _, v := range t.Leaves {
		v.print(w, indent, level+indent)
	}
}
