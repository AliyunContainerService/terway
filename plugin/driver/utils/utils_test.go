package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AliyunContainerService/terway/pkg/utils"
)

func TestJSONStr(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{
			name:     "string input",
			input:    "test",
			expected: `"test"`,
		},
		{
			name:     "struct input",
			input:    struct{ Name string }{Name: "test"},
			expected: `{"Name":"test"}`,
		},
		{
			name:     "nil input",
			input:    nil,
			expected: `null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := JSONStr(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGrabFileLock(t *testing.T) {
	lockfilePath := filepath.Join(utils.NormalizePath("/tmp"), "test.lock")

	// Clean up before test
	_ = os.Remove(lockfilePath)

	t.Run("successfully acquire lock", func(t *testing.T) {
		locker, err := GrabFileLock(lockfilePath)
		require.NoError(t, err)
		require.NotNil(t, locker)

		err = locker.Close()
		assert.NoError(t, err)
	})

	t.Run("acquire already held lock with timeout", func(t *testing.T) {
		// Acquire the lock
		locker1, err := GrabFileLock(lockfilePath)
		require.NoError(t, err)
		require.NotNil(t, locker1)

		// Try to acquire the same lock in a goroutine
		lockResult := make(chan *Locker, 1)
		errResult := make(chan error, 1)
		go func() {
			locker2, err := GrabFileLock(lockfilePath)
			lockResult <- locker2
			errResult <- err
		}()

		// Should timeout as the lock is held
		select {
		case <-time.After(12 * time.Second): // Longer than fileLockTimeOut
			// Expected to timeout
		case locker2 := <-lockResult:
			if locker2 != nil {
				_ = locker2.Close()
			}
			err := <-errResult
			// If we get here, the lock was acquired, which is unexpected
			if err == nil {
				t.Error("Expected to fail to acquire lock, but succeeded")
			}
		}

		// Release the lock
		err = locker1.Close()
		assert.NoError(t, err)
	})

	t.Run("invalid lock file path", func(t *testing.T) {
		locker, err := GrabFileLock("")
		if err == nil && locker != nil {
			_ = locker.Close()
		}
		// Depending on system behavior, this may or may not return an error
		// The important thing is that it doesn't panic
	})
}

func TestInitLog(t *testing.T) {
	t.Run("init debug log", func(t *testing.T) {
		// Redirect stderr to capture log output
		oldStderr := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		log := InitLog(true)
		log.Info("test message")

		w.Close()
		os.Stderr = oldStderr

		var buf [1024]byte
		n, _ := r.Read(buf[:])
		r.Close()

		// Check that something was written to the log
		assert.True(t, n > 0)

		// Clean up log file
		logFilePath := utils.NormalizePath("/var/log/terway.cni.log")
		_ = os.Remove(logFilePath)
	})

	t.Run("init non-debug log", func(t *testing.T) {
		log := InitLog(false)
		assert.NotNil(t, log)
	})
}

func TestLocker_Close(t *testing.T) {
	t.Run("close nil locker", func(t *testing.T) {
		locker := &Locker{}
		err := locker.Close()
		assert.NoError(t, err)
	})

	t.Run("close valid locker", func(t *testing.T) {
		lockfilePath := filepath.Join(utils.NormalizePath("/tmp"), "test2.lock")
		_ = os.Remove(lockfilePath)

		locker, err := GrabFileLock(lockfilePath)
		require.NoError(t, err)
		require.NotNil(t, locker)

		err = locker.Close()
		assert.NoError(t, err)

		// Should be able to acquire the lock again
		locker2, err := GrabFileLock(lockfilePath)
		assert.NoError(t, err)
		assert.NotNil(t, locker2)
		err = locker2.Close()
		assert.NoError(t, err)
	})
}
