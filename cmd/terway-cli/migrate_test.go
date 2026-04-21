package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolveTargetNodes(t *testing.T) {
	t.Parallel()

	newClient := func(t *testing.T, nodes ...*corev1.Node) *runtime.Scheme {
		t.Helper()
		scheme := runtime.NewScheme()
		require.NoError(t, clientgoscheme.AddToScheme(scheme))
		return scheme
	}

	tests := []struct {
		name     string
		nodesCSV string
		selector string
		cluster  []*corev1.Node
		want     []string
		wantErr  string
	}{
		{
			name:     "explicit nodes without selector",
			nodesCSV: "node-b,node-a,node-b",
			want:     []string{"node-b", "node-a"},
		},
		{
			name:     "selector only returns sorted matches",
			selector: "nodepool=eflo",
			cluster: []*corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-c", Labels: map[string]string{"nodepool": "eflo"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{"nodepool": "eflo"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-b", Labels: map[string]string{"nodepool": "other"}}},
			},
			want: []string{"node-a", "node-c"},
		},
		{
			name:     "explicit nodes are filtered by selector",
			nodesCSV: "node-b,node-a,node-c",
			selector: "env=prod",
			cluster: []*corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "node-a", Labels: map[string]string{"env": "prod"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-b", Labels: map[string]string{"env": "dev"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "node-c", Labels: map[string]string{"env": "prod"}}},
			},
			want: []string{"node-a", "node-c"},
		},
		{
			name:     "invalid selector returns error",
			selector: "env in (prod",
			wantErr:  "invalid --node-selector",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scheme := newClient(t)
			objs := make([]runtime.Object, 0, len(tt.cluster))
			for _, node := range tt.cluster {
				objs = append(objs, node)
			}
			k8s := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

			got, err := resolveTargetNodes(context.Background(), k8s, tt.nodesCSV, tt.selector)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
