package main

import (
	"fmt"
	"io"
)

type Tree struct {
	Key    string
	Value  string // value only exists in leaf
	IsLeaf bool
	Leaves []*Tree
}

// add a leaf to the tree
func (t *Tree) AddLeaf(path []string, value string) {
	if len(path) == 0 { // noop
		return
	}

	if len(path) == 1 { // is leaf
		t.Leaves = append(t.Leaves, &Tree{
			Key:    path[0],
			Value:  value,
			IsLeaf: true,
		})
		return
	}

	for _, v := range t.Leaves {
		if v.Key == path[0] && !v.IsLeaf {
			v.AddLeaf(path[1:], value)
			return
		}
	}

	tree := &Tree{
		Key:    path[0],
		IsLeaf: false,
		Leaves: nil,
	}
	t.Leaves = append(t.Leaves, tree)

	tree.AddLeaf(path[1:], value)
}

func (t *Tree) Print(w io.Writer, indent string, level string) {
	_, _ = fmt.Fprint(w, level)

	if t.IsLeaf {
		_, _ = fmt.Fprintf(w, "%s: %s\n", t.Key, t.Value)
		return
	}

	_, _ = fmt.Printf("%s:\n", t.Key)

	for _, v := range t.Leaves {
		v.Print(w, indent, level+indent)
	}
}
