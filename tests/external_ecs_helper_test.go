//go:build e2e

package tests

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/util/wait"
)

// ECSCommandExecutor executes commands on ECS instances via Aliyun RunCommand API
type ECSCommandExecutor struct {
	client   *ecs.Client
	regionID string
}

// ECSCommandResult represents the result of a command execution
type ECSCommandResult struct {
	Success    bool
	Output     string
	ExitCode   int64
	ErrorCode  string
	InstanceID string
	Duration   time.Duration
}

// NewECSCommandExecutor creates a new ECS command executor
// It reads credentials from environment variables:
// - ACCESS_KEY_ID
// - ACCESS_KEY_SECRET
// - REGION
func NewECSCommandExecutor() (*ECSCommandExecutor, error) {
	accessKey := os.Getenv("ACCESS_KEY_ID")
	accessKeySecret := os.Getenv("ACCESS_KEY_SECRET")
	regionID := os.Getenv("REGION")

	if accessKey == "" || accessKeySecret == "" || regionID == "" {
		return nil, fmt.Errorf("missing required environment variables: ACCESS_KEY_ID, ACCESS_KEY_SECRET, REGION")
	}

	client, err := ecs.NewClientWithAccessKey(regionID, accessKey, accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to create ECS client: %w", err)
	}

	return &ECSCommandExecutor{
		client:   client,
		regionID: regionID,
	}, nil
}

// GetExternalECSInstanceID returns the external ECS instance ID from environment variable
func GetExternalECSInstanceID() string {
	return os.Getenv("EXTERNAL_ECS_INSTANCE_ID")
}

// HasExternalECSConfig checks if external ECS configuration is available
func HasExternalECSConfig() bool {
	accessKey := os.Getenv("ACCESS_KEY_ID")
	accessKeySecret := os.Getenv("ACCESS_KEY_SECRET")
	regionID := os.Getenv("REGION")
	instanceID := GetExternalECSInstanceID()

	return accessKey != "" && accessKeySecret != "" && regionID != "" && instanceID != ""
}

// RunCommand executes a shell command on the specified ECS instance and returns the result
func (e *ECSCommandExecutor) RunCommand(ctx context.Context, instanceID, command string, timeout time.Duration) (*ECSCommandResult, error) {
	startTime := time.Now()

	request := ecs.CreateRunCommandRequest()
	request.Type = "RunShellScript"
	request.CommandContent = command
	request.Name = fmt.Sprintf("connectivity-test-%d", time.Now().UnixNano())
	request.InstanceId = &[]string{instanceID}
	request.Timeout = requests.NewInteger(int(timeout.Seconds()))

	response, err := e.client.RunCommand(request)
	if err != nil {
		return nil, fmt.Errorf("failed to run command: %w", err)
	}

	// Wait for command to complete and get result
	result, err := e.waitForCommandResult(ctx, response.InvokeId, instanceID, timeout)
	if err != nil {
		return nil, err
	}

	result.Duration = time.Since(startTime)
	return result, nil
}

// waitForCommandResult waits for the command to complete and retrieves the output
func (e *ECSCommandExecutor) waitForCommandResult(ctx context.Context, invokeID, instanceID string, timeout time.Duration) (*ECSCommandResult, error) {
	result := &ECSCommandResult{
		InstanceID: instanceID,
	}

	backoff := wait.Backoff{
		Duration: 2 * time.Second,
		Factor:   1.5,
		Steps:    20,
		Cap:      timeout,
	}

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		describeRequest := ecs.CreateDescribeInvocationResultsRequest()
		describeRequest.InvokeId = invokeID
		describeRequest.InstanceId = instanceID

		resp, err := e.client.DescribeInvocationResults(describeRequest)
		if err != nil {
			return false, nil // Retry on error
		}

		if len(resp.Invocation.InvocationResults.InvocationResult) == 0 {
			return false, nil // Retry if no results yet
		}

		invResult := resp.Invocation.InvocationResults.InvocationResult[0]

		// Check if command is still running
		switch invResult.InvocationStatus {
		case "Running", "Pending", "Scheduled":
			return false, nil // Keep waiting
		case "Success":
			result.Success = true
			result.ExitCode = invResult.ExitCode
			result.ErrorCode = invResult.ErrorCode
			// Output is base64 encoded
			if invResult.Output != "" {
				decoded, err := base64.StdEncoding.DecodeString(invResult.Output)
				if err != nil {
					result.Output = invResult.Output // Use raw if decode fails
				} else {
					result.Output = string(decoded)
				}
			}
			return true, nil
		case "Failed", "Stopped", "Timeout":
			result.Success = false
			result.ExitCode = invResult.ExitCode
			result.ErrorCode = invResult.ErrorCode
			if invResult.Output != "" {
				decoded, err := base64.StdEncoding.DecodeString(invResult.Output)
				if err != nil {
					result.Output = invResult.Output
				} else {
					result.Output = string(decoded)
				}
			}
			return true, nil
		default:
			return false, nil // Unknown status, keep waiting
		}
	})

	if err != nil {
		return nil, fmt.Errorf("timeout waiting for command result: %w", err)
	}

	return result, nil
}

// RunCurlCommand executes a curl command to test connectivity
func (e *ECSCommandExecutor) RunCurlCommand(ctx context.Context, instanceID, targetURL string, timeout time.Duration) (*ECSCommandResult, error) {
	// Use curl with timeout and silent mode, output full response
	command := fmt.Sprintf("curl -s --connect-timeout 5 -m 10 '%s'", targetURL)
	return e.RunCommand(ctx, instanceID, command, timeout)
}

// EchoResponse represents the parsed response from the echo server
type EchoResponse struct {
	Method     string
	Path       string
	Host       string
	RemoteAddr string
	LocalAddr  string
	RawOutput  string
}

// ParseEchoResponse parses the response from the echo server
// Expected format:
// GET /echo
// Host: 127.0.0.1
// RemoteAddr: 192.168.1.100:40928
// LocalAddr: 192.168.215.3/24
func ParseEchoResponse(output string) (*EchoResponse, error) {
	resp := &EchoResponse{
		RawOutput: output,
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	// Parse first line: "GET /echo" or similar
	firstLine := strings.TrimSpace(lines[0])
	parts := strings.SplitN(firstLine, " ", 2)
	if len(parts) >= 1 {
		resp.Method = parts[0]
	}
	if len(parts) >= 2 {
		resp.Path = parts[1]
	}

	// Parse header-like lines
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:colonIdx])
		value := strings.TrimSpace(line[colonIdx+1:])

		switch key {
		case "Host":
			resp.Host = value
		case "RemoteAddr":
			resp.RemoteAddr = value
		case "LocalAddr":
			resp.LocalAddr = value
		}
	}

	if resp.RemoteAddr == "" {
		return nil, fmt.Errorf("failed to parse RemoteAddr from response: %s", output)
	}

	return resp, nil
}

// GetSourceIP extracts the source IP from RemoteAddr (removes port if present)
func (r *EchoResponse) GetSourceIP() string {
	if r.RemoteAddr == "" {
		return ""
	}

	// RemoteAddr format is typically "IP:Port" or just "IP"
	// Handle IPv6 addresses like "[::1]:8080"
	if strings.HasPrefix(r.RemoteAddr, "[") {
		// IPv6 format: [ip]:port
		endBracket := strings.Index(r.RemoteAddr, "]")
		if endBracket != -1 {
			return r.RemoteAddr[1:endBracket]
		}
	}

	// Check if it contains a port (last colon not part of IPv6)
	lastColon := strings.LastIndex(r.RemoteAddr, ":")
	if lastColon != -1 {
		// Verify it's not an IPv6 address without port
		beforeColon := r.RemoteAddr[:lastColon]
		if !strings.Contains(beforeColon, ":") {
			// IPv4 with port
			return beforeColon
		}
	}

	return r.RemoteAddr
}

// ConnectivityTestResult represents the result of a connectivity test
type ConnectivityTestResult struct {
	ScenarioName       string
	Source             string
	Target             string
	ServiceType        string
	TrafficPolicy      string
	BackendLocation    string
	LoadBalancerType   string
	Connected          bool
	SourceIPPreserved  bool
	ObservedSourceIP   string
	ExpectedSourceIP   string
	ErrorMessage       string
	Duration           time.Duration
	RawResponse        string
}

// String returns a formatted string representation of the test result
func (r *ConnectivityTestResult) String() string {
	status := "FAIL"
	if r.Connected {
		status = "OK"
	}

	preserved := "N/A"
	if r.Connected {
		if r.SourceIPPreserved {
			preserved = "Yes"
		} else {
			preserved = "No"
		}
	}

	return fmt.Sprintf("[%s] %s | SourceIP: %s (preserved: %s) | Duration: %v",
		status, r.ScenarioName, r.ObservedSourceIP, preserved, r.Duration)
}

// ConnectivityTestReport holds all test results for reporting
type ConnectivityTestReport struct {
	Results   []*ConnectivityTestResult
	StartTime time.Time
	EndTime   time.Time
}

// NewConnectivityTestReport creates a new test report
func NewConnectivityTestReport() *ConnectivityTestReport {
	return &ConnectivityTestReport{
		Results:   make([]*ConnectivityTestResult, 0),
		StartTime: time.Now(),
	}
}

// AddResult adds a test result to the report
func (r *ConnectivityTestReport) AddResult(result *ConnectivityTestResult) {
	r.Results = append(r.Results, result)
}

// Finalize sets the end time of the report
func (r *ConnectivityTestReport) Finalize() {
	r.EndTime = time.Now()
}

// GenerateMarkdownReport generates a markdown formatted report
func (r *ConnectivityTestReport) GenerateMarkdownReport() string {
	var sb strings.Builder

	sb.WriteString("# Connectivity Test Report\n\n")
	sb.WriteString(fmt.Sprintf("**Start Time:** %s\n", r.StartTime.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("**End Time:** %s\n", r.EndTime.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("**Duration:** %v\n\n", r.EndTime.Sub(r.StartTime)))

	// Summary
	passed := 0
	failed := 0
	for _, result := range r.Results {
		if result.Connected {
			passed++
		} else {
			failed++
		}
	}
	sb.WriteString(fmt.Sprintf("**Summary:** %d passed, %d failed, %d total\n\n", passed, failed, len(r.Results)))

	// Results table
	sb.WriteString("## Results\n\n")
	sb.WriteString("| Test Case | Client | Service Type | ExternalTrafficPolicy | Backend Location | Status | Observed Source IP | IP Preserved | Notes |\n")
	sb.WriteString("|-----------|--------|--------------|----------------------|------------------|--------|-------------------|--------------|-------|\n")

	for _, result := range r.Results {
		status := "FAIL"
		if result.Connected {
			status = "OK"
		}

		preserved := "N/A"
		if result.Connected {
			if result.SourceIPPreserved {
				preserved = "Yes"
			} else {
				preserved = "No"
			}
		}

		details := result.ErrorMessage
		if details == "" && result.Connected {
			if result.SourceIPPreserved {
				details = "Original IP preserved"
			} else {
				details = "SNAT applied"
			}
		}

		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			result.ScenarioName,
			result.Source,
			result.Target,
			result.TrafficPolicy,
			result.BackendLocation,
			status,
			result.ObservedSourceIP,
			preserved,
			details,
		))
	}

	return sb.String()
}

// IsPrivateIP checks if an IP address is a private/internal IP
func IsPrivateIP(ip string) bool {
	// Common private IP ranges
	privatePatterns := []string{
		`^10\.`,
		`^172\.(1[6-9]|2[0-9]|3[01])\.`,
		`^192\.168\.`,
		`^100\.(6[4-9]|[7-9][0-9]|1[0-1][0-9]|12[0-7])\.`, // CGNAT range
		`^fc`, // IPv6 ULA
		`^fd`, // IPv6 ULA
	}

	for _, pattern := range privatePatterns {
		if matched, _ := regexp.MatchString(pattern, ip); matched {
			return true
		}
	}

	return false
}

// GetECSPrivateIP retrieves the private IP of an ECS instance
func (e *ECSCommandExecutor) GetECSPrivateIP(ctx context.Context, instanceID string) (string, error) {
	request := ecs.CreateDescribeInstancesRequest()
	request.InstanceIds = fmt.Sprintf(`["%s"]`, instanceID)

	response, err := e.client.DescribeInstances(request)
	if err != nil {
		return "", fmt.Errorf("failed to describe instance: %w", err)
	}

	if len(response.Instances.Instance) == 0 {
		return "", fmt.Errorf("instance not found: %s", instanceID)
	}

	instance := response.Instances.Instance[0]

	// Try to get VPC private IP first
	if len(instance.VpcAttributes.PrivateIpAddress.IpAddress) > 0 {
		return instance.VpcAttributes.PrivateIpAddress.IpAddress[0], nil
	}

	// Fallback to inner IP
	if len(instance.InnerIpAddress.IpAddress) > 0 {
		return instance.InnerIpAddress.IpAddress[0], nil
	}

	return "", fmt.Errorf("no private IP found for instance: %s", instanceID)
}
