package credential

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	"github.com/agiledragon/gomonkey/v2"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	os.Setenv("ECS_ENDPOINT", "test")
	os.Setenv("X-ACSPROXY-ASCM-CONTEXT", "X-ACSPROXY-ASCM-CONTEXT")
	cfg := ClientConfig{RegionID: "cn-test", Domain: "domain"}
	cred := ProviderV1(&fakeProvider{})
	client, err := NewECSClient(cfg, cred)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewECSV2Client(t *testing.T) {
	os.Setenv("ECS_ENDPOINT", "test")
	cfg := ClientConfig{RegionID: "cn-test", Domain: "domain"}
	cred := ProviderV2(&fakeProvider{})
	client, err := NewECSV2Client(cfg, cred)
	if err != nil && !errors.Is(err, nil) { // allow for not implemented
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil && err == nil {
		t.Fatal("expected non-nil client or error")
	}
}

func TestNewVPCClient(t *testing.T) {
	os.Setenv("VPC_ENDPOINT", "test")
	cfg := ClientConfig{RegionID: "cn-test", Domain: "domain"}
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
	os.Setenv("EFLO_ENDPOINT", "test")
	os.Setenv("EFLO_REGION_ID", "test")
	cfg := ClientConfig{RegionID: "cn-test", Domain: "domain"}
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
	cfg := ClientConfig{RegionID: "cn-test", Domain: "domain"}
	cred := ProviderV2(&fakeProvider{})
	client, err := NewEFLOV2Client(cfg, cred)
	if err != nil && !errors.Is(err, nil) { // allow for not implemented
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil && err == nil {
		t.Fatal("expected non-nil client or error")
	}
}

func TestNewEFLOControllerClient(t *testing.T) {
	os.Setenv("EFLO_ENDPOINT", "")
	os.Setenv("EFLO_REGION_ID", "")
	cfg := ClientConfig{RegionID: "cn-test", Domain: "domain"}
	cred := ProviderV2(&fakeProvider{})
	client, err := NewEFLOControllerClient(cfg, cred)
	if err != nil && !errors.Is(err, nil) { // allow for not implemented
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil && err == nil {
		t.Fatal("expected non-nil client or error")
	}
}

func TestInitializeClientMgr(t *testing.T) {
	c, err := InitializeClientMgr("cn-test", &fakeProvider{})
	require.NoError(t, err)
	require.NotNil(t, c)

	// test client
	require.NotNil(t, c.ECS())
	require.NotNil(t, c.VPC())
	require.NotNil(t, c.EFLO())
	require.NotNil(t, c.EFLOV2())
	require.NotNil(t, c.EFLOController())
	require.NotNil(t, c.ECSV2())
}

func TestV1Warp_GetCredentials(t *testing.T) {
	tests := []struct {
		name          string
		mockCred      *provider.Credentials
		mockError     error
		expectError   bool
		expectedAK    string
		expectedSK    string
		expectedToken string
	}{
		{
			name: "success - valid credentials",
			mockCred: &provider.Credentials{
				AccessKeyId:     "test-ak-123",
				AccessKeySecret: "test-sk-456",
				SecurityToken:   "test-token-789",
			},
			mockError:     nil,
			expectError:   false,
			expectedAK:    "test-ak-123",
			expectedSK:    "test-sk-456",
			expectedToken: "test-token-789",
		},
		{
			name: "success - credentials without security token",
			mockCred: &provider.Credentials{
				AccessKeyId:     "test-ak-abc",
				AccessKeySecret: "test-sk-def",
				SecurityToken:   "",
			},
			mockError:     nil,
			expectError:   false,
			expectedAK:    "test-ak-abc",
			expectedSK:    "test-sk-def",
			expectedToken: "",
		},
		{
			name:        "error - provider returns error",
			mockCred:    nil,
			mockError:   errors.New("failed to get credentials from provider"),
			expectError: true,
		},
		{
			name:        "error - context timeout",
			mockCred:    nil,
			mockError:   context.DeadlineExceeded,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock provider
			mockProvider := &fakeProvider{}

			// Patch the Credentials method
			patches := gomonkey.ApplyMethod(mockProvider, "Credentials",
				func(_ *fakeProvider, ctx context.Context) (*provider.Credentials, error) {
					return tt.mockCred, tt.mockError
				})
			defer patches.Reset()

			// Create V1Warp instance
			v1warp := &V1Warp{providers: mockProvider}

			// Call GetCredentials
			cred, err := v1warp.GetCredentials()

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, cred)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, cred)
				assert.Equal(t, tt.expectedAK, cred.AccessKeyId)
				assert.Equal(t, tt.expectedSK, cred.AccessKeySecret)
				assert.Equal(t, tt.expectedToken, cred.SecurityToken)
			}
		})
	}
}

func TestV1Warp_GetProviderName(t *testing.T) {
	mockProvider := &fakeProvider{}
	v1warp := &V1Warp{providers: mockProvider}

	name := v1warp.GetProviderName()
	assert.Equal(t, "chaining", name)
}

func TestHeaderTransport_RoundTrip(t *testing.T) {
	tests := []struct {
		name            string
		headers         map[string]string
		expectedHeaders map[string]string
	}{
		{
			name: "success - single header",
			headers: map[string]string{
				"X-Custom-Header": "custom-value",
			},
			expectedHeaders: map[string]string{
				"X-Custom-Header": "custom-value",
			},
		},
		{
			name: "success - multiple headers",
			headers: map[string]string{
				"X-Custom-Header-1": "value-1",
				"X-Custom-Header-2": "value-2",
				"Authorization":     "Bearer token",
			},
			expectedHeaders: map[string]string{
				"X-Custom-Header-1": "value-1",
				"X-Custom-Header-2": "value-2",
				"Authorization":     "Bearer token",
			},
		},
		{
			name:            "success - no headers",
			headers:         map[string]string{},
			expectedHeaders: map[string]string{},
		},
		{
			name: "success - ACSPROXY header",
			headers: map[string]string{
				"x-acsproxy-ascm-context": "test-context",
			},
			expectedHeaders: map[string]string{
				"x-acsproxy-ascm-context": "test-context",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server to verify headers
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify all expected headers are present
				for key, expectedValue := range tt.expectedHeaders {
					actualValue := r.Header.Get(key)
					assert.Equal(t, expectedValue, actualValue, "Header %s should match", key)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			// Create headerTransport
			transport := &headerTransport{headers: tt.headers}

			// Create a request
			req, err := http.NewRequest("GET", server.URL, nil)
			assert.NoError(t, err)

			// Execute RoundTrip
			resp, err := transport.RoundTrip(req)
			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			if resp.Body != nil {
				resp.Body.Close()
			}
		})
	}
}

func TestV1Warp_Integration(t *testing.T) {
	// Test the integration between V1Warp and credentials.CredentialsProvider interface
	mockProvider := &fakeProvider{}
	patches := gomonkey.ApplyMethod(mockProvider, "Credentials",
		func(_ *fakeProvider, ctx context.Context) (*provider.Credentials, error) {
			return &provider.Credentials{
				AccessKeyId:     "integration-ak",
				AccessKeySecret: "integration-sk",
				SecurityToken:   "integration-token",
			}, nil
		})
	defer patches.Reset()

	v1warp := &V1Warp{providers: mockProvider}

	// Test GetCredentials
	cred, err := v1warp.GetCredentials()
	assert.NoError(t, err)
	assert.NotNil(t, cred)
	assert.Equal(t, "integration-ak", cred.AccessKeyId)
	assert.Equal(t, "integration-sk", cred.AccessKeySecret)
	assert.Equal(t, "integration-token", cred.SecurityToken)

	// Test GetProviderName
	name := v1warp.GetProviderName()
	assert.Equal(t, "chaining", name)

	// Verify it implements credentials.CredentialsProvider
	var _ credentials.CredentialsProvider = v1warp
}
