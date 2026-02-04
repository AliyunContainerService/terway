package client

import (
	"context"
	"testing"
)

func TestGetBackendAPI_EmptyContext(t *testing.T) {
	ctx := context.Background()
	got := GetBackendAPI(ctx)
	if got != BackendAPIECS {
		t.Errorf("GetBackendAPI(empty ctx) = %v, want BackendAPIECS", got)
	}
}

func TestSetBackendAPI_GetBackendAPI(t *testing.T) {
	ctx := context.Background()
	ctx = SetBackendAPI(ctx, BackendAPIEFLO)
	if got := GetBackendAPI(ctx); got != BackendAPIEFLO {
		t.Errorf("GetBackendAPI after SetBackendAPI(EFLO) = %v, want BackendAPIEFLO", got)
	}

	ctx = SetBackendAPI(context.Background(), BackendAPIEFLOHDENI)
	if got := GetBackendAPI(ctx); got != BackendAPIEFLOHDENI {
		t.Errorf("GetBackendAPI after SetBackendAPI(EFLOHDENI) = %v, want BackendAPIEFLOHDENI", got)
	}
}
