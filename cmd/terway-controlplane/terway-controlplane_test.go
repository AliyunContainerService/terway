package main

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/terway/types/controlplane"
)

func Test_detectMultiIP(t *testing.T) {

	type args struct {
		ctx          context.Context
		directClient client.Client
		cfg          *controlplane.Config
	}
	tests := []struct {
		name      string
		args      args
		wantErr   bool
		checkFunc func(t *testing.T, cfg *controlplane.Config)
	}{
		{
			name: "test empty config",
			args: args{
				ctx: context.Background(),
				directClient: func() client.Client {
					nodeReader := fake.NewClientBuilder()
					return nodeReader.Build()
				}(),
				cfg: &controlplane.Config{},
			},
			wantErr:   false,
			checkFunc: func(t *testing.T, cfg *controlplane.Config) {},
		},
		{
			name: "test config not found",
			args: args{
				ctx: context.Background(),
				directClient: func() client.Client {
					nodeReader := fake.NewClientBuilder()
					return nodeReader.Build()
				}(),
				cfg: &controlplane.Config{
					Controllers: []string{"multi-ip-node", "node", "multi-ip-pod"},
				},
			},
			wantErr:   true,
			checkFunc: func(t *testing.T, cfg *controlplane.Config) {},
		},
		{
			name: "test config ipamv1",
			args: args{
				ctx: context.Background(),
				directClient: func() client.Client {
					nodeReader := fake.NewClientBuilder()
					nodeReader.WithObjects(
						&corev1.ConfigMap{
							ObjectMeta: metav1.ObjectMeta{Name: "eni-config", Namespace: "kube-system"},
							Data: map[string]string{
								"eni_conf": "{\"ipam_type\":\"\"}",
							},
						},
					)
					return nodeReader.Build()
				}(),
				cfg: &controlplane.Config{
					Controllers: []string{"multi-ip-node", "node", "multi-ip-pod", "foo"},
				},
			},
			wantErr: false,
			checkFunc: func(t *testing.T, cfg *controlplane.Config) {
				assert.False(t, lo.Contains(cfg.Controllers, "multi-ip-node"))
				assert.False(t, lo.Contains(cfg.Controllers, "node"))
				assert.False(t, lo.Contains(cfg.Controllers, "multi-ip-pod"))
				assert.True(t, lo.Contains(cfg.Controllers, "foo"))
			},
		},
		{
			name: "test config ipamv2",
			args: args{
				ctx: context.Background(),
				directClient: func() client.Client {
					nodeReader := fake.NewClientBuilder()
					nodeReader.WithObjects(
						&corev1.ConfigMap{
							ObjectMeta: metav1.ObjectMeta{Name: "eni-config", Namespace: "kube-system"},
							Data: map[string]string{
								"eni_conf": "{\"ipam_type\":\"crd\"}",
							},
						},
					)
					return nodeReader.Build()
				}(),
				cfg: &controlplane.Config{
					Controllers: []string{"multi-ip-node", "node", "multi-ip-pod", "foo"},
				},
			},
			wantErr: false,
			checkFunc: func(t *testing.T, cfg *controlplane.Config) {
				assert.True(t, lo.Contains(cfg.Controllers, "multi-ip-node"))
				assert.True(t, lo.Contains(cfg.Controllers, "node"))
				assert.True(t, lo.Contains(cfg.Controllers, "multi-ip-pod"))
				assert.True(t, lo.Contains(cfg.Controllers, "foo"))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := detectMultiIP(tt.args.ctx, tt.args.directClient, tt.args.cfg); (err != nil) != tt.wantErr {
				t.Errorf("detectMultiIP() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				tt.checkFunc(t, tt.args.cfg)
			}
		})
	}
}
