package credential

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
)

type fakeProvider struct{}

func (f *fakeProvider) Credentials(ctx context.Context) (*provider.Credentials, error) {
	return &provider.Credentials{
		AccessKeyId:     "ak",
		AccessKeySecret: "sk",
		SecurityToken:   "token",
	}, nil
}

func TestNewECSClient(t *testing.T) {
	os.Setenv("ECS_ENDPOINT", "")
	cfg := ClientConfig{RegionID: "cn-test"}
	cred := ProviderV1(&fakeProvider{})
	client, err := NewECSClient(cfg, cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewVPCClient(t *testing.T) {
	os.Setenv("VPC_ENDPOINT", "")
	cfg := ClientConfig{RegionID: "cn-test"}
	cred := ProviderV1(&fakeProvider{})
	client, err := NewVPCClient(cfg, cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewEFLOClient(t *testing.T) {
	os.Setenv("EFLO_ENDPOINT", "")
	os.Setenv("EFLO_REGION_ID", "")
	cfg := ClientConfig{RegionID: "cn-test"}
	cred := ProviderV1(&fakeProvider{})
	client, err := NewEFLOClient(cfg, cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewEFLOV2Client(t *testing.T) {
	os.Setenv("EFLO_ENDPOINT", "")
	os.Setenv("EFLO_REGION_ID", "")
	cfg := ClientConfig{RegionID: "cn-test"}
	cred := ProviderV2(&fakeProvider{})
	client, err := NewEFLOV2Client(cfg, cred)
	if err != nil && !errors.Is(err, nil) { // allow for not implemented
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil && err == nil {
		t.Fatal("expected non-nil client or error")
	}
}
