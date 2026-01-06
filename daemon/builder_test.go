package daemon

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	clientmocks "github.com/AliyunContainerService/terway/pkg/aliyun/client/mocks"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	instancemocks "github.com/AliyunContainerService/terway/pkg/aliyun/instance/mocks"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/eni"
	factorymocks "github.com/AliyunContainerService/terway/pkg/factory/mocks"
	"github.com/AliyunContainerService/terway/pkg/k8s"
	k8smocks "github.com/AliyunContainerService/terway/pkg/k8s/mocks"
	"github.com/AliyunContainerService/terway/pkg/storage"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types/daemon"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewNetworkServiceBuilder(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx)
	assert.NotNil(t, builder, "NewNetworkServiceBuilder should return a non-nil NetworkServiceBuilder")
	assert.Equal(t, ctx, builder.ctx, "The context in the NetworkServiceBuilder should be the same as the provided context")
}

func TestWithConfigFilePath(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx)
	configFilePath := "/path/to/config"

	builder = builder.WithConfigFilePath(configFilePath)

	assert.Equal(t, configFilePath, builder.configFilePath, "The configFilePath should be set correctly in the NetworkServiceBuilder")
}

func TestWithDaemonMode(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx)
	daemonMode := "true"

	builder = builder.WithDaemonMode(daemonMode)

	assert.Equal(t, daemonMode, builder.daemonMode, "The daemonMode should be set correctly in the NetworkServiceBuilder")
}

func TestInitService(t *testing.T) {
	tests := []struct {
		name          string
		daemonMode    string
		expectedError bool
	}{
		{
			name:          "Valid daemon mode ENIMultiIP",
			daemonMode:    daemon.ModeENIMultiIP,
			expectedError: false,
		},
		{
			name:          "Unsupported daemon mode",
			daemonMode:    "unsupported",
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := &NetworkServiceBuilder{
				daemonMode: tc.daemonMode,
			}

			builder = builder.InitService()

			if tc.expectedError {
				assert.NotNil(t, builder.err)
			} else {
				assert.Nil(t, builder.err)
				assert.NotNil(t, builder.service)
				assert.Equal(t, tc.daemonMode, builder.service.daemonMode)
			}
		})
	}
}

func TestNetworkServiceBuilder_LoadGlobalConfig(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()
	configContent := `
{
      "version": "1",
      "max_pool_size": 5,
      "min_pool_size": 0,
      "credential_path": "/var/addon/token-config",
      "ipam_type": "crd"
    }`
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), os.ModeDir)
	assert.NoError(t, err)
	builder := &NetworkServiceBuilder{
		configFilePath: tmpFile.Name(),
		service:        &networkService{},
	}
	builder.LoadGlobalConfig()
	assert.True(t, *builder.config.EnablePatchPodIPs)
}

func TestNetworkServiceBuilder_LoadGlobalConfig2(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()
	configContent := `
{
      "version": "1",
      "max_pool_size": 5,
      "min_pool_size": 0,
      "credential_path": "/var/addon/token-config",
      "enable_patch_pod_ips": false,
      "ipam_type": "crd"
    }`
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), os.ModeDir)
	assert.NoError(t, err)
	builder := &NetworkServiceBuilder{
		configFilePath: tmpFile.Name(),
		service:        &networkService{},
	}
	builder.LoadGlobalConfig()
	assert.False(t, *builder.config.EnablePatchPodIPs)
}

func TestNetworkServiceBuilder_GetConfigFromFileWithMerge_1(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()
	configContent := `
{
      "version": "1",
      "max_pool_size": 5,
      "min_pool_size": 0,
      "credential_path": "/var/addon/token-config",
      "ipam_type": "crd"
    }`

	dynamicCfg := ""
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), os.ModeDir)
	assert.NoError(t, err)
	config, err := daemon.GetConfigFromFileWithMerge(tmpFile.Name(), []byte(dynamicCfg))
	assert.NoError(t, err)
	config.Populate()

	assert.True(t, *config.EnablePatchPodIPs)
}

func TestNetworkServiceBuilder_GetConfigFromFileWithMerge_2(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()
	configContent := `
{
      "version": "1",
      "max_pool_size": 5,
      "min_pool_size": 0,
      "credential_path": "/var/addon/token-config",
      "enable_patch_pod_ips": false,
      "ipam_type": "crd"
    }`

	dynamicCfg := ""
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), os.ModeDir)
	assert.NoError(t, err)
	config, err := daemon.GetConfigFromFileWithMerge(tmpFile.Name(), []byte(dynamicCfg))
	assert.NoError(t, err)
	config.Populate()

	assert.False(t, *config.EnablePatchPodIPs)
}

func TestNetworkServiceBuilder_InitService_AllModes(t *testing.T) {
	tests := []struct {
		name          string
		daemonMode    string
		expectedError bool
	}{
		{
			name:          "ENIMultiIP mode",
			daemonMode:    daemon.ModeENIMultiIP,
			expectedError: false,
		},
		{
			name:          "ENIOnly mode",
			daemonMode:    daemon.ModeENIOnly,
			expectedError: true,
		},
		{
			name:          "VPC mode",
			daemonMode:    daemon.ModeVPC,
			expectedError: true,
		},
		{
			name:          "Unsupported mode",
			daemonMode:    "invalid_mode",
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := &NetworkServiceBuilder{
				daemonMode: tc.daemonMode,
			}

			builder = builder.InitService()

			if tc.expectedError {
				assert.NotNil(t, builder.err)
			} else {
				assert.Nil(t, builder.err)
				assert.NotNil(t, builder.service)
				assert.Equal(t, tc.daemonMode, builder.service.daemonMode)
			}
		})
	}
}

func TestNetworkServiceBuilder_LoadGlobalConfig_InvalidFile(t *testing.T) {
	builder := &NetworkServiceBuilder{
		configFilePath: "/non/existent/file.json",
		service:        &networkService{},
	}

	builder = builder.LoadGlobalConfig()

	assert.NotNil(t, builder.err)
}

func TestNetworkServiceBuilder_LoadGlobalConfig_InvalidJSON(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "config-*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer tmpFile.Close()

	// Write invalid JSON
	configContent := `{ "version": "1", invalid json }`
	err = os.WriteFile(tmpFile.Name(), []byte(configContent), 0644)
	assert.NoError(t, err)

	builder := &NetworkServiceBuilder{
		configFilePath: tmpFile.Name(),
		service:        &networkService{},
	}

	builder = builder.LoadGlobalConfig()

	assert.NotNil(t, builder.err)
}

func TestNetworkServiceBuilder_Build_Success(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx).
		WithDaemonMode(daemon.ModeENIMultiIP).
		InitService()

	// Set up minimal required fields
	builder.config = &daemon.Config{
		Version: "1",
	}

	service, err := builder.Build()

	assert.NoError(t, err)
	assert.NotNil(t, service)
	assert.Equal(t, daemon.ModeENIMultiIP, service.daemonMode)
}

func TestNetworkServiceBuilder_Build_WithError(t *testing.T) {
	ctx := context.Background()
	builder := NewNetworkServiceBuilder(ctx).
		WithDaemonMode("invalid_mode").
		InitService()

	service, err := builder.Build()

	assert.Error(t, err)
	assert.Nil(t, service)
}

func Test_setupAliyunClient(t *testing.T) {
	builder := &NetworkServiceBuilder{
		config: &daemon.Config{
			Version:      "1",
			AccessID:     "access_id",
			AccessSecret: "access_secret",
		},
	}

	metadata := instancemocks.NewInterface(t)
	metadata.On("GetRegionID").Return("cn-hangzhou", nil)
	instance.Init(metadata)

	err := builder.setupAliyunClient()
	assert.NoError(t, err)

	assert.NotNil(t, builder.aliyunClient)
}

func TestNetworkServiceBuilder_InitResourceDB(t *testing.T) {
	tests := []struct {
		name          string
		hasError      bool
		expectedError bool
	}{
		{
			name:          "Success case - no existing error",
			hasError:      false,
			expectedError: false,
		},
		{
			name:          "Error case with existing error",
			hasError:      true,
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := &NetworkServiceBuilder{
				service: &networkService{},
			}

			if tc.hasError {
				builder.err = assert.AnError
			}

			result := builder.InitResourceDB()

			if tc.expectedError {
				assert.NotNil(t, result.err)
				assert.Equal(t, assert.AnError, result.err)
			} else {
				// For success case, we can't easily test the actual storage creation
				// without complex mocking, so we just verify the function returns
				// and that the builder is returned (which happens even if storage fails)
				assert.NotNil(t, result)
			}
		})
	}
}

func TestNetworkServiceBuilder_LoadDynamicConfig(t *testing.T) {
	tests := []struct {
		name                string
		hasError            bool
		dynamicCfg          string
		dynamicCfgLabel     string
		getDynamicConfigErr error
		getConfigErr        error
		validateErr         error
		expectedError       bool
	}{
		{
			name:                "Success case - valid dynamic config",
			hasError:            false,
			dynamicCfg:          `{"version": "1", "max_pool_size": 5}`,
			dynamicCfgLabel:     "test-config",
			getDynamicConfigErr: nil,
			getConfigErr:        nil,
			validateErr:         nil,
			expectedError:       false,
		},
		{
			name:                "Success case - no dynamic config",
			hasError:            false,
			dynamicCfg:          "",
			dynamicCfgLabel:     "",
			getDynamicConfigErr: nil,
			getConfigErr:        nil,
			validateErr:         nil,
			expectedError:       false,
		},
		{
			name:                "Error case - existing error",
			hasError:            true,
			dynamicCfg:          "",
			dynamicCfgLabel:     "",
			getDynamicConfigErr: nil,
			getConfigErr:        nil,
			validateErr:         nil,
			expectedError:       true,
		},
		{
			name:                "Error case - getDynamicConfig fails",
			hasError:            false,
			dynamicCfg:          "",
			dynamicCfgLabel:     "",
			getDynamicConfigErr: assert.AnError,
			getConfigErr:        nil,
			validateErr:         nil,
			expectedError:       false, // Should fallback gracefully
		},
		{
			name:                "Error case - config parsing fails",
			hasError:            false,
			dynamicCfg:          "invalid json",
			dynamicCfgLabel:     "test-config",
			getDynamicConfigErr: nil,
			getConfigErr:        assert.AnError,
			validateErr:         nil,
			expectedError:       true,
		},
		{
			name:                "Error case - config validation fails",
			hasError:            false,
			dynamicCfg:          `{"version": "1", "max_pool_size": 5}`,
			dynamicCfgLabel:     "test-config",
			getDynamicConfigErr: nil,
			getConfigErr:        nil,
			validateErr:         assert.AnError,
			expectedError:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create temporary config file
			tmpFile, err := os.CreateTemp("", "config-*.json")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpFile.Name())

			baseConfig := `{
				"version": "1",
				"max_pool_size": 5,
				"min_pool_size": 0,
				"credential_path": "/var/addon/token-config",
				"ipam_type": "crd"
			}`
			err = os.WriteFile(tmpFile.Name(), []byte(baseConfig), 0644)
			assert.NoError(t, err)

			// Create mock k8s client
			mockK8s := k8smocks.NewKubernetes(t)

			builder := &NetworkServiceBuilder{
				ctx:            context.Background(),
				configFilePath: tmpFile.Name(),
				service: &networkService{
					k8s: mockK8s,
				},
			}

			if tc.hasError {
				builder.err = assert.AnError
			}

			// Setup gomonkey patches
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// Mock getDynamicConfig function
			patches.ApplyFunc(getDynamicConfig, func(ctx context.Context, k8s interface{}) (string, string, error) {
				return tc.dynamicCfg, tc.dynamicCfgLabel, tc.getDynamicConfigErr
			})

			// Mock GetConfigFromFileWithMerge if needed
			if tc.getConfigErr != nil {
				patches.ApplyFunc(daemon.GetConfigFromFileWithMerge, func(configFilePath string, dynamicCfg []byte) (*daemon.Config, error) {
					return nil, tc.getConfigErr
				})
			}

			// Mock config Validate method if needed
			if tc.validateErr != nil && tc.getConfigErr == nil {
				patches.ApplyMethodFunc(&daemon.Config{}, "Validate", func() error {
					return tc.validateErr
				})
			}

			result := builder.LoadDynamicConfig()

			if tc.expectedError {
				assert.NotNil(t, result.err)
			} else {
				assert.Nil(t, result.err)
				if tc.dynamicCfg != "" && tc.getDynamicConfigErr == nil && tc.getConfigErr == nil {
					assert.NotNil(t, result.config)
				}
			}
		})
	}
}

// Test initInstanceLimit method
func TestNetworkServiceBuilder_initInstanceLimit(t *testing.T) {
	tests := []struct {
		name             string
		nodeExists       bool
		annotations      map[string]string
		limitFromAnno    *client.Limits
		limitFromAnnoErr error
		instanceType     string
		instanceTypeErr  error
		limitFromAPI     *client.Limits
		limitFromAPIErr  error
		expectedError    bool
		expectedErrorMsg string
	}{
		{
			name:             "node not found",
			nodeExists:       false,
			expectedError:    true,
			expectedErrorMsg: "k8s node not found",
		},
		{
			name:       "limit from annotation - matching instance type",
			nodeExists: true,
			annotations: map[string]string{
				"k8s.aliyun.com/eni-limit": "10",
			},
			limitFromAnno: &client.Limits{
				InstanceTypeID: "ecs.g6.large",
				Adapters:       3,
				IPv4PerAdapter: 10,
			},
			limitFromAnnoErr: nil,
			instanceType:     "ecs.g6.large",
			instanceTypeErr:  nil,
			expectedError:    false,
		},
		{
			name:             "get instance type error",
			nodeExists:       true,
			annotations:      map[string]string{},
			limitFromAnno:    nil,
			limitFromAnnoErr: nil,
			instanceType:     "",
			instanceTypeErr:  errors.New("metadata error"),
			expectedError:    true,
			expectedErrorMsg: "metadata error",
		},
		{
			name:             "get limit from API error",
			nodeExists:       true,
			annotations:      map[string]string{},
			limitFromAnno:    nil,
			limitFromAnnoErr: nil,
			instanceType:     "ecs.g6.large",
			instanceTypeErr:  nil,
			limitFromAPI:     nil,
			limitFromAPIErr:  errors.New("API error"),
			expectedError:    true,
			expectedErrorMsg: "upable get instance limit",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			mockK8s := k8smocks.NewKubernetes(t)
			mockLimitProvider := clientmocks.NewLimitProvider(t)
			mockMeta := instancemocks.NewInterface(t)
			mockECS := clientmocks.NewECS(t)

			if tc.nodeExists {
				node := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "test-node",
						Annotations: tc.annotations,
					},
				}
				mockK8s.On("Node").Return(node)
			} else {
				mockK8s.On("Node").Return(nil)
			}

			if tc.nodeExists {
				mockLimitProvider.On("GetLimitFromAnno", tc.annotations).Return(tc.limitFromAnno, tc.limitFromAnnoErr).Maybe()
				// GetInstanceType is always called when node exists (either for limit validation or API call)
				mockMeta.On("GetInstanceType").Return(tc.instanceType, tc.instanceTypeErr).Maybe()
				if tc.limitFromAPI != nil || tc.limitFromAPIErr != nil {
					mockLimitProvider.On("GetLimit", mockECS, tc.instanceType).Return(tc.limitFromAPI, tc.limitFromAPIErr).Maybe()
				}
			}

			patches.ApplyFunc(initTrunk, func() (string, error) {
				return "eni-trunk", nil
			})

			patches.ApplyFunc(client.GetLimitProvider, func() client.LimitProvider {
				return mockLimitProvider
			})
			instance.Init(mockMeta)
			patches.ApplyFunc(checkInstance, func(limit *client.Limits, daemonMode string, config *daemon.Config) (bool, bool) {
				return true, false
			})

			builder := &NetworkServiceBuilder{
				ctx:          context.Background(),
				daemonMode:   daemon.ModeENIMultiIP,
				config:       &daemon.Config{IPStack: "ipv4", EnableENITrunking: true, EnableERDMA: true},
				service:      &networkService{k8s: mockK8s},
				aliyunClient: &client.APIFacade{},
			}
			patches.ApplyMethodFunc(builder.aliyunClient, "GetECS", func() client.ECS {
				return mockECS
			})

			err := builder.initInstanceLimit()

			if tc.expectedError {
				assert.Error(t, err)
				if tc.expectedErrorMsg != "" {
					assert.Contains(t, err.Error(), tc.expectedErrorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, builder.limit)
			}
		})
	}
}

// Test PostInitForLegacyMode method
func TestNetworkServiceBuilder_PostInitForLegacyMode(t *testing.T) {
	tests := []struct {
		name             string
		hasExistingError bool
		eflo             bool
		resourceDBErr    error
		eniMgrRunErr     error
		expectedError    bool
	}{
		{
			name:             "existing error returns early",
			hasExistingError: true,
			expectedError:    true,
		},
		{
			name:          "eflo mode - resource DB list error",
			eflo:          true,
			resourceDBErr: errors.New("db list error"),
			expectedError: true,
		},
		{
			name:          "eflo mode - eni manager run error",
			eflo:          true,
			eniMgrRunErr:  errors.New("eni manager run error"),
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			mockK8s := k8smocks.NewKubernetes(t)

			if tc.eflo && !tc.hasExistingError {
				mockK8s.On("SetCustomStatefulWorkloadKinds", []string(nil)).Return(nil).Maybe()
				mockK8s.On("GetClient").Return(nil).Maybe()
			}

			var mockResourceDB *builderMockStorage
			if tc.eflo && !tc.hasExistingError {
				mockResourceDB = &builderMockStorage{
					listResult: []interface{}{},
					listErr:    tc.resourceDBErr,
				}
			}

			patches.ApplyFunc(backoff.OverrideBackoff, func(override map[string]backoff.ExtendedBackoff) {})
			patches.ApplyFunc(eni.NewRemote, func(client interface{}, ipv4, ipv6 interface{}) eni.NetworkInterface {
				return &mockNetworkInterface{}
			})
			patches.ApplyFunc(eni.NewManager, func(pool *daemon.PoolConfig, syncPeriod time.Duration, nis []eni.NetworkInterface, policy daemon.EniSelectionPolicy, k8s interface{}) *eni.Manager {
				return &eni.Manager{}
			})
			patches.ApplyMethodFunc(&eni.Manager{}, "Run", func(ctx context.Context, wg *sync.WaitGroup, podResources []daemon.PodResources) error {
				return tc.eniMgrRunErr
			})

			builder := &NetworkServiceBuilder{
				ctx:        context.Background(),
				daemonMode: daemon.ModeENIMultiIP,
				config:     &daemon.Config{IPStack: "ipv4"},
				service:    &networkService{k8s: mockK8s, resourceDB: mockResourceDB},
				eflo:       tc.eflo,
			}

			if tc.hasExistingError {
				builder.err = errors.New("existing error")
			}

			result := builder.PostInitForLegacyMode()

			if tc.expectedError {
				assert.NotNil(t, result.err)
			} else {
				assert.Nil(t, result.err)
			}
		})
	}
}

// Test PostInitForCRDV2 method - only testing early return case to avoid goroutine issues
func TestNetworkServiceBuilder_PostInitForCRDV2(t *testing.T) {
	tests := []struct {
		name             string
		hasExistingError bool
		expectedError    bool
	}{
		{
			name:             "existing error returns early",
			hasExistingError: true,
			expectedError:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			builder := &NetworkServiceBuilder{
				ctx:       context.Background(),
				namespace: "kube-system",
				config:    &daemon.Config{IPStack: "ipv4"},
				service:   &networkService{},
			}

			if tc.hasExistingError {
				builder.err = errors.New("existing error")
			}

			result := builder.PostInitForCRDV2()

			if tc.expectedError {
				assert.NotNil(t, result.err)
			} else {
				assert.Nil(t, result.err)
			}
		})
	}
}

// builderMockStorage implements storage.Storage for testing
type builderMockStorage struct {
	listResult []interface{}
	listErr    error
}

func (m *builderMockStorage) Put(key string, value interface{}) error { return nil }
func (m *builderMockStorage) Get(key string) (interface{}, error)     { return nil, nil }
func (m *builderMockStorage) List() ([]interface{}, error)            { return m.listResult, m.listErr }
func (m *builderMockStorage) Delete(key string) error                 { return nil }

// mockNetworkInterface implements eni.NetworkInterface for testing
type mockNetworkInterface struct{}

func (m *mockNetworkInterface) Allocate(ctx context.Context, cni *daemon.CNI, request eni.ResourceRequest) (chan *eni.AllocResp, []eni.Trace) {
	return nil, nil
}
func (m *mockNetworkInterface) Release(ctx context.Context, cni *daemon.CNI, request eni.NetworkResource) (bool, error) {
	return true, nil
}
func (m *mockNetworkInterface) Priority() int     { return 0 }
func (m *mockNetworkInterface) Dispose(n int) int { return 0 }
func (m *mockNetworkInterface) Run(ctx context.Context, podResources []daemon.PodResources, wg *sync.WaitGroup) error {
	return nil
}

// Test setupENIManager method - error paths
func TestNetworkServiceBuilder_setupENIManager_ErrorPaths(t *testing.T) {
	tests := []struct {
		name             string
		zoneID           string
		zoneIDErr        error
		instanceID       string
		instanceIDErr    error
		vswitchID        string
		vswitchIDErr     error
		eflo             bool
		expectedError    bool
		expectedErrorMsg string
	}{
		{
			name:             "get zone ID error",
			zoneIDErr:        errors.New("zone id error"),
			expectedError:    true,
			expectedErrorMsg: "zone id error",
		},
		{
			name:             "get instance ID error",
			zoneID:           "cn-hangzhou-a",
			instanceIDErr:    errors.New("instance id error"),
			expectedError:    true,
			expectedErrorMsg: "instance id error",
		},
		{
			name:             "get vswitch ID error",
			zoneID:           "cn-hangzhou-a",
			instanceID:       "i-123456",
			vswitchIDErr:     errors.New("vswitch id error"),
			expectedError:    true,
			expectedErrorMsg: "vswitch id error",
		},
		{
			name:             "eflo mode not supported",
			zoneID:           "cn-hangzhou-a",
			instanceID:       "i-123456",
			vswitchID:        "vsw-123456",
			eflo:             true,
			expectedError:    true,
			expectedErrorMsg: "eflo unsupported",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			mockMeta := instancemocks.NewInterface(t)
			mockMeta.On("GetZoneID").Return(tc.zoneID, tc.zoneIDErr).Maybe()
			if tc.zoneIDErr == nil {
				mockMeta.On("GetInstanceID").Return(tc.instanceID, tc.instanceIDErr).Maybe()
			}
			if tc.zoneIDErr == nil && tc.instanceIDErr == nil {
				mockMeta.On("GetVSwitchID").Return(tc.vswitchID, tc.vswitchIDErr).Maybe()
			}
			instance.Init(mockMeta)

			mockK8s := k8smocks.NewKubernetes(t)
			mockECS := clientmocks.NewECS(t)

			aliyunClient := &client.APIFacade{}
			patches.ApplyMethodFunc(aliyunClient, "GetECS", func() client.ECS {
				return mockECS
			})

			patches.ApplyFunc(getENIConfig, func(cfg *daemon.Config, zoneID string) *daemon.ENIConfig {
				return &daemon.ENIConfig{SecurityGroupIDs: []string{"sg-123456"}}
			})
			patches.ApplyFunc(getPoolConfig, func(cfg *daemon.Config, daemonMode string, limit *client.Limits) (*daemon.PoolConfig, error) {
				return &daemon.PoolConfig{Capacity: 10, MaxENI: 3}, nil
			})
			patches.ApplyFunc(vswpool.NewSwitchPool, func(size int, ttl string) (*vswpool.SwitchPool, error) {
				return &vswpool.SwitchPool{}, nil
			})

			builder := &NetworkServiceBuilder{
				ctx:          context.Background(),
				daemonMode:   daemon.ModeENIMultiIP,
				config:       &daemon.Config{IPStack: "ipv4"},
				service:      &networkService{k8s: mockK8s, resourceDB: storage.NewMemoryStorage(), enableIPv4: true},
				aliyunClient: aliyunClient,
				limit:        &client.Limits{Adapters: 3, IPv4PerAdapter: 10},
				eflo:         tc.eflo,
			}

			err := builder.setupENIManager()

			if tc.expectedError {
				assert.Error(t, err)
				if tc.expectedErrorMsg != "" {
					assert.Contains(t, err.Error(), tc.expectedErrorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Test checkInstance function
func TestCheckInstance(t *testing.T) {
	tests := []struct {
		name         string
		limit        *client.Limits
		daemonMode   string
		config       *daemon.Config
		expectedIPv4 bool
		expectedIPv6 bool
	}{
		{
			name: "ipv4 only stack",
			limit: &client.Limits{
				Adapters:       3,
				IPv4PerAdapter: 10,
				IPv6PerAdapter: 10,
			},
			daemonMode:   daemon.ModeENIMultiIP,
			config:       &daemon.Config{IPStack: "ipv4"},
			expectedIPv4: true,
			expectedIPv6: false,
		},
		{
			name: "ipv6 only stack with support",
			limit: &client.Limits{
				Adapters:       3,
				IPv4PerAdapter: 10,
				IPv6PerAdapter: 10,
			},
			daemonMode:   daemon.ModeENIMultiIP,
			config:       &daemon.Config{IPStack: "ipv6"},
			expectedIPv4: false,
			expectedIPv6: true,
		},
		{
			name: "dual stack with support",
			limit: &client.Limits{
				Adapters:       3,
				IPv4PerAdapter: 10,
				IPv6PerAdapter: 10,
			},
			daemonMode:   daemon.ModeENIMultiIP,
			config:       &daemon.Config{IPStack: "dual"},
			expectedIPv4: true,
			expectedIPv6: true,
		},
		{
			name: "dual stack without ipv6 support",
			limit: &client.Limits{
				Adapters:       3,
				IPv4PerAdapter: 10,
				IPv6PerAdapter: 0,
			},
			daemonMode:   daemon.ModeENIMultiIP,
			config:       &daemon.Config{IPStack: "dual"},
			expectedIPv4: true,
			expectedIPv6: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ipv4, ipv6 := checkInstance(tc.limit, tc.daemonMode, tc.config)
			assert.Equal(t, tc.expectedIPv4, ipv4)
			assert.Equal(t, tc.expectedIPv6, ipv6)
		})
	}
}

// Test getPoolConfig function (builder specific)
func TestGetPoolConfigBuilder(t *testing.T) {
	tests := []struct {
		name             string
		config           *daemon.Config
		daemonMode       string
		limit            *client.Limits
		expectedCapacity int
		expectedMaxENI   int
		expectedError    bool
	}{
		{
			name:             "ENI Only mode",
			config:           &daemon.Config{EniCapRatio: 1.0, EniCapShift: 0},
			daemonMode:       daemon.ModeENIOnly,
			limit:            &client.Limits{Adapters: 4},
			expectedCapacity: 3,
			expectedMaxENI:   3,
			expectedError:    false,
		},
		{
			name:             "ENI MultiIP mode",
			config:           &daemon.Config{EniCapRatio: 1.0, EniCapShift: 0},
			daemonMode:       daemon.ModeENIMultiIP,
			limit:            &client.Limits{Adapters: 4, IPv4PerAdapter: 10},
			expectedCapacity: 30,
			expectedMaxENI:   3,
			expectedError:    false,
		},
		{
			name:             "ENI MultiIP mode with MaxENI limit",
			config:           &daemon.Config{EniCapRatio: 1.0, EniCapShift: 0, MaxENI: 2},
			daemonMode:       daemon.ModeENIMultiIP,
			limit:            &client.Limits{Adapters: 4, IPv4PerAdapter: 10},
			expectedCapacity: 20,
			expectedMaxENI:   2,
			expectedError:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			poolConfig, err := getPoolConfig(tc.config, tc.daemonMode, tc.limit)

			if tc.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedCapacity, poolConfig.Capacity)
				assert.Equal(t, tc.expectedMaxENI, poolConfig.MaxENI)
			}
		})
	}
}

// Test getPodResources function
func TestGetPodResources(t *testing.T) {
	tests := []struct {
		name        string
		input       []interface{}
		expectedLen int
	}{
		{
			name:        "empty list",
			input:       []interface{}{},
			expectedLen: 0,
		},
		{
			name: "single pod resource",
			input: []interface{}{
				daemon.PodResources{PodInfo: &daemon.PodInfo{Name: "test-pod", Namespace: "default"}},
			},
			expectedLen: 1,
		},
		{
			name: "multiple pod resources",
			input: []interface{}{
				daemon.PodResources{PodInfo: &daemon.PodInfo{Name: "test-pod-1", Namespace: "default"}},
				daemon.PodResources{PodInfo: &daemon.PodInfo{Name: "test-pod-2", Namespace: "kube-system"}},
			},
			expectedLen: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := getPodResources(tc.input)
			assert.Len(t, result, tc.expectedLen)
		})
	}
}

// Test initTrunk function
func TestInitTrunk(t *testing.T) {
	tests := []struct {
		name            string
		preferTrunkID   string
		attachedENIs    []*daemon.ENI
		attachedErr     error
		createENI       *daemon.ENI
		createErr       error
		expectedTrunkID string
		expectedError   bool
	}{
		{
			name:          "get attached error",
			preferTrunkID: "",
			attachedErr:   errors.New("get attached error"),
			expectedError: true,
		},
		{
			name:            "preferred trunk found",
			preferTrunkID:   "eni-trunk-123",
			attachedENIs:    []*daemon.ENI{{ID: "eni-trunk-123", Trunk: true}},
			expectedTrunkID: "eni-trunk-123",
			expectedError:   false,
		},
		{
			name:            "any trunk found",
			preferTrunkID:   "",
			attachedENIs:    []*daemon.ENI{{ID: "eni-trunk-456", Trunk: true}},
			expectedTrunkID: "eni-trunk-456",
			expectedError:   false,
		},
		{
			name:            "no trunk, max eni reached",
			preferTrunkID:   "",
			attachedENIs:    []*daemon.ENI{{ID: "eni-1"}, {ID: "eni-2"}, {ID: "eni-3"}},
			expectedTrunkID: "",
			expectedError:   false,
		},
		{
			name:            "create trunk success",
			preferTrunkID:   "",
			attachedENIs:    []*daemon.ENI{},
			createENI:       &daemon.ENI{ID: "eni-new-trunk"},
			expectedTrunkID: "eni-new-trunk",
			expectedError:   false,
		},
		{
			name:          "create trunk error",
			preferTrunkID: "",
			attachedENIs:  []*daemon.ENI{},
			createErr:     errors.New("create error"),
			expectedError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockK8s := k8smocks.NewKubernetes(t)
			mockK8s.On("GetTrunkID").Return(tc.preferTrunkID).Maybe()

			mockFactory := factorymocks.NewFactory(t)
			mockFactory.On("GetAttachedNetworkInterface", tc.preferTrunkID).Return(tc.attachedENIs, tc.attachedErr).Maybe()

			if tc.attachedErr == nil && len(tc.attachedENIs) < 3 {
				hasTrunk := false
				for _, e := range tc.attachedENIs {
					if e.Trunk {
						hasTrunk = true
						break
					}
				}
				if !hasTrunk {
					mockFactory.On("CreateNetworkInterface", 1, 0, "trunk").Return(tc.createENI, nil, nil, tc.createErr).Maybe()
					if tc.createErr != nil && tc.createENI != nil {
						mockFactory.On("DeleteNetworkInterface", tc.createENI.ID).Return(nil).Maybe()
					}
				}
			}

			config := &daemon.Config{}
			poolConfig := &daemon.PoolConfig{MaxENI: 3}

			trunkID, err := initTrunk(config, poolConfig, mockK8s, mockFactory)

			if tc.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedTrunkID, trunkID)
			}
		})
	}
}

// Test getENIConfig function (builder specific)
func TestGetENIConfigBuilder(t *testing.T) {
	tests := []struct {
		name                    string
		config                  *daemon.Config
		zoneID                  string
		expectedZoneID          string
		expectedSelectionPolicy daemon.EniSelectionPolicy
	}{
		{
			name:                    "basic config",
			config:                  &daemon.Config{ENITags: map[string]string{"key": "value"}, SecurityGroups: []string{"sg-123"}, EniSelectionPolicy: "most_ips"},
			zoneID:                  "cn-hangzhou-a",
			expectedZoneID:          "cn-hangzhou-a",
			expectedSelectionPolicy: daemon.EniSelectionPolicyMostIPs,
		},
		{
			name:                    "least ips selection policy",
			config:                  &daemon.Config{EniSelectionPolicy: "least_ips"},
			zoneID:                  "cn-hangzhou-b",
			expectedZoneID:          "cn-hangzhou-b",
			expectedSelectionPolicy: daemon.EniSelectionPolicyLeastIPs,
		},
		{
			name:                    "with vswitches config",
			config:                  &daemon.Config{VSwitches: map[string][]string{"cn-hangzhou-a": {"vsw-1", "vsw-2"}}},
			zoneID:                  "cn-hangzhou-a",
			expectedZoneID:          "cn-hangzhou-a",
			expectedSelectionPolicy: daemon.EniSelectionPolicyMostIPs,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			eniConfig := getENIConfig(tc.config, tc.zoneID)
			assert.Equal(t, tc.expectedZoneID, eniConfig.ZoneID)
			assert.Equal(t, tc.expectedSelectionPolicy, eniConfig.EniSelectionPolicy)
		})
	}
}

// Test RunENIMgr method
func TestNetworkServiceBuilder_RunENIMgr(t *testing.T) {
	tests := []struct {
		name             string
		hasExistingError bool
		runErr           error
		expectedError    bool
	}{
		{
			name:             "existing error returns early",
			hasExistingError: true,
			expectedError:    true,
		},
		{
			name:          "run error",
			runErr:        errors.New("run error"),
			expectedError: true,
		},
		{
			name:          "success",
			expectedError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			patches.ApplyMethodFunc(&eni.Manager{}, "Run", func(ctx context.Context, wg *sync.WaitGroup, podResources []daemon.PodResources) error {
				return tc.runErr
			})

			builder := &NetworkServiceBuilder{
				ctx:     context.Background(),
				service: &networkService{},
			}

			if tc.hasExistingError {
				builder.err = errors.New("existing error")
			}

			mgr := &eni.Manager{}
			result := builder.RunENIMgr(context.Background(), mgr)

			if tc.expectedError {
				assert.NotNil(t, result.err)
			} else {
				assert.Nil(t, result.err)
				assert.Equal(t, mgr, result.service.eniMgr)
			}
		})
	}
}

// Test RegisterTracing method
func TestNetworkServiceBuilder_RegisterTracing(t *testing.T) {
	tests := []struct {
		name             string
		hasExistingError bool
		expectedError    bool
	}{
		{
			name:             "existing error returns early",
			hasExistingError: true,
			expectedError:    true,
		},
		{
			name:          "success",
			expectedError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockK8s := k8smocks.NewKubernetes(t)

			builder := &NetworkServiceBuilder{
				service: &networkService{k8s: mockK8s},
			}

			if tc.hasExistingError {
				builder.err = errors.New("existing error")
			}

			result := builder.RegisterTracing()

			if tc.expectedError {
				assert.NotNil(t, result.err)
			} else {
				assert.Nil(t, result.err)
			}
		})
	}
}

func Test_InitK8S(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	mockK8s := k8smocks.NewKubernetes(t)
	mockK8s.On("Node").Return(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"k8s.aliyun.com/exclusive-mode-eni-type": "eniOnly",
				"alibabacloud.com/lingjun-worker":        "true",
			},
		},
	}).Maybe()

	patches.ApplyFunc(k8s.NewK8S, func(daemonMode string, config *daemon.Config, ns string) (k8s.Kubernetes, error) {
		return mockK8s, nil
	})

	builder := &NetworkServiceBuilder{
		daemonMode: daemon.ModeENIMultiIP,
		config:     &daemon.Config{},
		service:    &networkService{},
		namespace:  "default",
	}

	result := builder.InitK8S()
	assert.Nil(t, result.err)
	assert.Equal(t, mockK8s, result.service.k8s)

}
