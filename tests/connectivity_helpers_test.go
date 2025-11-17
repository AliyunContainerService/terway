//go:build e2e

package tests

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/wait"
)

// ConnectivityTestConfig holds configuration for connectivity tests
type ConnectivityTestConfig struct {
	Timeout  time.Duration
	Interval time.Duration
	Retries  int
}

// DefaultConnectivityTestConfig returns the default configuration for connectivity tests
func DefaultConnectivityTestConfig() ConnectivityTestConfig {
	return ConnectivityTestConfig{
		Timeout:  60 * time.Second,
		Interval: 1 * time.Second,
		Retries:  3,
	}
}

// PullRequest holds information about a pull/curl request
type PullRequest struct {
	Namespace string
	PodName   string
	Container string
	Target    string
	IPv6      bool
}

// String returns a formatted string representation of the pull request
func (pr *PullRequest) String() string {
	mode := "IPv4"
	if pr.IPv6 {
		mode = "IPv6"
	}
	return fmt.Sprintf("PullRequest{Namespace: %s, Pod: %s, Container: %s, Target: %s, Mode: %s}",
		pr.Namespace, pr.PodName, pr.Container, pr.Target, mode)
}

// PullResponse holds information about a pull/curl response
type PullResponse struct {
	StatusLine string
	FullOutput string
	Duration   time.Duration
	Attempts   int
	Success    bool
}

// Pull executes a connectivity test by running curl from a pod to a target
// It returns detailed information about the request and response
func Pull(client klient.Client, namespace, podName, container, target string, t *testing.T) (PullResponse, error) {
	req := &PullRequest{
		Namespace: namespace,
		PodName:   podName,
		Container: container,
		Target:    target,
		IPv6:      false,
	}
	return PullWithConfig(client, req, DefaultConnectivityTestConfig(), t)
}

// PullWithIPv6 executes a connectivity test with IPv6 support
func PullWithIPv6(client klient.Client, namespace, podName, container, target string, t *testing.T) (PullResponse, error) {
	req := &PullRequest{
		Namespace: namespace,
		PodName:   podName,
		Container: container,
		Target:    target,
		IPv6:      true,
	}
	return PullWithConfig(client, req, DefaultConnectivityTestConfig(), t)
}

// PullWithConfig executes a connectivity test with custom configuration
func PullWithConfig(client klient.Client, req *PullRequest, config ConnectivityTestConfig, t *testing.T) (PullResponse, error) {
	resp := PullResponse{}
	errors := []error{}
	startTime := time.Now()
	attempts := 0

	t.Logf("🔍 Starting connectivity test: %s", req.String())

	err := wait.For(func(ctx context.Context) (done bool, err error) {
		attempts++
		var stdout, stderr bytes.Buffer

		cmd := []string{"curl", "-m", "2", "--retry", fmt.Sprintf("%d", config.Retries), "-I"}

		// Add IPv6 support
		if req.IPv6 {
			cmd = append(cmd, "-6", "-g")
		}

		cmd = append(cmd, req.Target)

		t.Logf("  [Attempt %d] Executing command: %v", attempts, cmd)

		execErr := client.Resources().ExecInPod(ctx, req.Namespace, req.PodName, req.Container, cmd, &stdout, &stderr)
		if execErr != nil {
			errMsg := fmt.Sprintf("failed to execute curl: %v", execErr)
			if stderr.String() != "" {
				errMsg += fmt.Sprintf(" (stderr: %s)", stderr.String())
			}
			t.Logf("  [Attempt %d] ❌ Execution error: %s", attempts, errMsg)
			errors = append(errors, fmt.Errorf("%s", errMsg))
			return false, nil
		}

		output := stdout.String()
		statusLine := strings.Split(output, "\n")[0]

		t.Logf("  [Attempt %d] Response status: %s", attempts, statusLine)

		resp.StatusLine = statusLine
		resp.FullOutput = output

		if !strings.Contains(statusLine, "200") {
			t.Logf("  [Attempt %d] ⏳ Retrying (expected 200, got: %s)", attempts, statusLine)
			errors = append(errors, fmt.Errorf("expected HTTP 200, got: %s", statusLine))
			return false, nil
		}

		t.Logf("  [Attempt %d] ✓ Success: HTTP 200 received", attempts)
		return true, nil
	},
		wait.WithTimeout(config.Timeout),
		wait.WithInterval(config.Interval))

	duration := time.Since(startTime)
	resp.Duration = duration
	resp.Attempts = attempts

	if err != nil {
		resp.Success = false
		errMsg := fmt.Sprintf("connectivity test failed for %s after %d attempts (duration: %v)",
			req.Target, attempts, duration)
		t.Logf("❌ %s", errMsg)

		if len(errors) > 0 {
			t.Logf("Errors encountered:")
			for i, e := range errors {
				t.Logf("  [Error %d] %v", i+1, e)
			}
		}

		return resp, utilerrors.NewAggregate(errors)
	}

	resp.Success = true
	t.Logf("✓ Connectivity test succeeded for %s after %d attempts (duration: %v)",
		req.Target, attempts, duration)

	return resp, nil
}

// PullFail executes a connectivity test that expects failure
func PullFail(client klient.Client, namespace, podName, container, target string, t *testing.T) (PullResponse, error) {
	req := &PullRequest{
		Namespace: namespace,
		PodName:   podName,
		Container: container,
		Target:    target,
		IPv6:      false,
	}
	return PullFailWithConfig(client, req, DefaultConnectivityTestConfig(), t)
}

// PullFailWithConfig executes a connectivity test that expects failure with custom configuration
func PullFailWithConfig(client klient.Client, req *PullRequest, config ConnectivityTestConfig, t *testing.T) (PullResponse, error) {
	resp := PullResponse{}
	errors := []error{}
	startTime := time.Now()
	attempts := 0

	t.Logf("🔍 Starting negative connectivity test (expecting failure): %s", req.String())

	err := wait.For(func(ctx context.Context) (done bool, err error) {
		attempts++
		var stdout, stderr bytes.Buffer

		cmd := []string{"curl", "-m", "2", "--retry", fmt.Sprintf("%d", config.Retries), "-I", req.Target}

		t.Logf("  [Attempt %d] Executing command: %v", attempts, cmd)

		execErr := client.Resources().ExecInPod(ctx, req.Namespace, req.PodName, req.Container, cmd, &stdout, &stderr)
		if execErr != nil {
			t.Logf("  [Attempt %d] ✓ Expected failure: curl execution failed: %v", attempts, execErr)
			resp.StatusLine = "Connection Failed"
			resp.FullOutput = execErr.Error()
			return true, nil
		}

		output := stdout.String()
		statusLine := strings.Split(output, "\n")[0]

		t.Logf("  [Attempt %d] ❌ Unexpected success: %s", attempts, statusLine)
		errors = append(errors, fmt.Errorf("expected connection to fail but got success with status: %s", statusLine))

		resp.StatusLine = statusLine
		resp.FullOutput = output

		return false, nil
	},
		wait.WithTimeout(config.Timeout),
		wait.WithInterval(config.Interval))

	duration := time.Since(startTime)
	resp.Duration = duration
	resp.Attempts = attempts

	if err != nil {
		resp.Success = false
		errMsg := fmt.Sprintf("negative connectivity test failed for %s after %d attempts (duration: %v) - connection succeeded when it should have failed",
			req.Target, attempts, duration)
		t.Logf("❌ %s", errMsg)

		if len(errors) > 0 {
			t.Logf("Errors encountered:")
			for i, e := range errors {
				t.Logf("  [Error %d] %v", i+1, e)
			}
		}

		return resp, utilerrors.NewAggregate(errors)
	}

	resp.Success = true
	t.Logf("✓ Negative connectivity test succeeded for %s after %d attempts (duration: %v) - connection failed as expected",
		req.Target, attempts, duration)

	return resp, nil
}
