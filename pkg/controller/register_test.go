package register

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func TestAdd(t *testing.T) {
	// Clear existing controllers for test isolation
	originalControllers := make(map[string]struct {
		Creator Creator
		Enable  bool
	})
	for k, v := range Controllers {
		originalControllers[k] = v
	}
	defer func() {
		Controllers = originalControllers
	}()

	// Clear Controllers for clean test
	Controllers = make(map[string]struct {
		Creator Creator
		Enable  bool
	})

	// Test adding a controller
	creator := func(mgr manager.Manager, ctrlCtx *ControllerCtx) error {
		return nil
	}

	Add("test-controller", creator, true)

	// Verify controller was added
	ctrl, exists := Controllers["test-controller"]
	assert.True(t, exists)
	assert.NotNil(t, ctrl.Creator)
	assert.True(t, ctrl.Enable)

	// Test adding another controller with enable=false
	Add("test-controller-2", creator, false)
	ctrl2, exists := Controllers["test-controller-2"]
	assert.True(t, exists)
	assert.False(t, ctrl2.Enable)

	// Test overwriting existing controller
	creator2 := func(mgr manager.Manager, ctrlCtx *ControllerCtx) error {
		return nil
	}
	Add("test-controller", creator2, false)
	ctrl3, exists := Controllers["test-controller"]
	assert.True(t, exists)
	assert.NotNil(t, ctrl3.Creator)
	assert.False(t, ctrl3.Enable)
}

func TestControllerCtx(t *testing.T) {
	ctx := context.Background()
	ctrlCtx := &ControllerCtx{
		Context: ctx,
	}

	// Test that ControllerCtx embeds context.Context
	assert.Equal(t, ctx, ctrlCtx.Context)

	// Test that ControllerCtx can be used as context
	select {
	case <-ctrlCtx.Done():
		t.Error("context should not be done")
	default:
		// Expected
	}
}
