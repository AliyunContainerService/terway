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
// Logic:
// 1. If connection succeeds (HTTP 200) → FAIL immediately (unexpected success)
// 2. If connection fails → retry once to confirm it's not flaky
// 3. If both attempts fail due to curl error (connection refused/timeout) → SUCCESS (expected failure)
// 4. If failure is due to kubectl exec API timeout → FAIL (infrastructure issue)
func PullFailWithConfig(client klient.Client, req *PullRequest, config ConnectivityTestConfig, t *testing.T) (PullResponse, error) {
	resp := PullResponse{}
	startTime := time.Now()

	t.Logf("🔍 Starting negative connectivity test (expecting failure): %s", req.String())

	// Helper function to execute curl and check result
	execCurl := func(attempt int) (succeeded bool, isCurlFailure bool, statusLine string, err error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var stdout, stderr bytes.Buffer
		// Use shorter timeout for curl since we expect it to fail
		cmd := []string{"curl", "-m", "5", "-I", req.Target}

		t.Logf("  [Attempt %d] Executing command: %v", attempt, cmd)

		execErr := client.Resources().ExecInPod(ctx, req.Namespace, req.PodName, req.Container, cmd, &stdout, &stderr)
		if execErr != nil {
			// Check if it's an API/infrastructure error (not curl failure)
			errStr := execErr.Error()
			if strings.Contains(errStr, "i/o timeout") && strings.Contains(errStr, "6443") {
				// kubectl API timeout - infrastructure issue
				return false, false, "", fmt.Errorf("kubectl exec API timeout (infrastructure issue): %v", execErr)
			}
			// curl failed to connect - this is expected
			t.Logf("  [Attempt %d] ✓ curl failed (expected): %v", attempt, execErr)
			return false, true, "Connection Failed", nil
		}

		// curl succeeded - check response
		output := stdout.String()
		statusLine = strings.Split(output, "\n")[0]
		t.Logf("  [Attempt %d] Response: %s", attempt, statusLine)

		return true, false, statusLine, nil
	}

	// First attempt
	succeeded, isCurlFailure, statusLine, err := execCurl(1)
	if err != nil {
		// Infrastructure error
		resp.Duration = time.Since(startTime)
		resp.Attempts = 1
		resp.Success = false
		t.Logf("❌ Infrastructure error: %v", err)
		return resp, err
	}

	if succeeded {
		// Connection succeeded - test should FAIL immediately
		resp.Duration = time.Since(startTime)
		resp.Attempts = 1
		resp.Success = false
		resp.StatusLine = statusLine
		errMsg := fmt.Sprintf("negative connectivity test FAILED: connection succeeded with %s (expected to fail)", statusLine)
		t.Logf("❌ %s", errMsg)
		return resp, fmt.Errorf("%s", errMsg)
	}

	if isCurlFailure {
		// First attempt failed as expected, retry once to confirm it's not flaky
		t.Logf("  [Attempt 1] Connection failed, retrying once to confirm...")
		time.Sleep(2 * time.Second)

		succeeded2, isCurlFailure2, statusLine2, err2 := execCurl(2)
		if err2 != nil {
			// Infrastructure error on retry
			resp.Duration = time.Since(startTime)
			resp.Attempts = 2
			resp.Success = false
			t.Logf("❌ Infrastructure error on retry: %v", err2)
			return resp, err2
		}

		if succeeded2 {
			// Second attempt succeeded - first failure was flaky, test should FAIL
			resp.Duration = time.Since(startTime)
			resp.Attempts = 2
			resp.Success = false
			resp.StatusLine = statusLine2
			errMsg := fmt.Sprintf("negative connectivity test FAILED: connection succeeded on retry with %s (first failure was flaky)", statusLine2)
			t.Logf("❌ %s", errMsg)
			return resp, fmt.Errorf("%s", errMsg)
		}

		if isCurlFailure2 {
			// Both attempts failed - test PASSED (expected behavior)
			resp.Duration = time.Since(startTime)
			resp.Attempts = 2
			resp.Success = true
			resp.StatusLine = "Connection Failed"
			t.Logf("✓ Negative connectivity test PASSED: connection failed on both attempts (as expected)")
			return resp, nil
		}
	}

	// Should not reach here
	resp.Duration = time.Since(startTime)
	resp.Attempts = 1
	resp.Success = false
	return resp, fmt.Errorf("unexpected state in PullFail")
}
