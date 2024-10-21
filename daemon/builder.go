package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/AliyunContainerService/terway/pkg/aliyun/client"
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	eni2 "github.com/AliyunContainerService/terway/pkg/aliyun/eni"
	"github.com/AliyunContainerService/terway/pkg/aliyun/instance"
	"github.com/AliyunContainerService/terway/pkg/backoff"
	"github.com/AliyunContainerService/terway/pkg/eni"
	"github.com/AliyunContainerService/terway/pkg/factory"
	"github.com/AliyunContainerService/terway/pkg/factory/aliyun"
	"github.com/AliyunContainerService/terway/pkg/k8s"
	"github.com/AliyunContainerService/terway/pkg/storage"
	"github.com/AliyunContainerService/terway/pkg/tracing"
	"github.com/AliyunContainerService/terway/pkg/utils"
	vswpool "github.com/AliyunContainerService/terway/pkg/vswitch"
	"github.com/AliyunContainerService/terway/types"
	"github.com/AliyunContainerService/terway/types/daemon"
)

type NetworkServiceBuilder struct {
	ctx            context.Context
	configFilePath string
	config         *daemon.Config
	daemonMode     string
	service        *networkService
	aliyunClient   *client.OpenAPI

	limit *client.Limits

	err error
}

func NewNetworkServiceBuilder(ctx context.Context) *NetworkServiceBuilder {
	return &NetworkServiceBuilder{ctx: ctx}
}

func (b *NetworkServiceBuilder) WithConfigFilePath(configFilePath string) *NetworkServiceBuilder {
	b.configFilePath = configFilePath
	return b
}

func (b *NetworkServiceBuilder) WithDaemonMode(daemonMode string) *NetworkServiceBuilder {
	b.daemonMode = daemonMode
	return b
}

func (b *NetworkServiceBuilder) InitService() *NetworkServiceBuilder {
	if b.err != nil {
		return b
	}
	b.service = &networkService{
		configFilePath: b.configFilePath,
		pendingPods:    sync.Map{},
	}
	switch b.daemonMode {
	case daemon.ModeENIMultiIP:
		b.service.daemonMode = b.daemonMode
	default:
		b.err = fmt.Errorf("unsupported daemon mode")
		return b
	}
	return b
}

func (b *NetworkServiceBuilder) LoadGlobalConfig() *NetworkServiceBuilder {
	if b.err != nil {
		return b
	}
	globalConfig, err := daemon.GetConfigFromFileWithMerge(b.configFilePath, nil)
	if err != nil {
		b.err = err
		return b
	}
	switch globalConfig.IPStack {
	case "ipv4":
		b.service.enableIPv4 = true
	case "dual":
		b.service.enableIPv4 = true
		b.service.enableIPv6 = true
	case "ipv6":
		b.service.enableIPv6 = true
	}
	b.config = globalConfig

	b.service.ipamType = globalConfig.IPAMType

	return b
}

func (b *NetworkServiceBuilder) InitK8S() *NetworkServiceBuilder {
	if b.err != nil {
		return b
	}
	var err error
	b.service.k8s, err = k8s.NewK8S(b.daemonMode, b.config)
	if err != nil {
		b.err = fmt.Errorf("error init k8s: %w", err)
		return b
	}

	if types.NodeExclusiveENIMode(b.service.k8s.Node().Labels) == types.ExclusiveENIOnly {
		b.service.daemonMode = daemon.ModeENIOnly
	}

	return b
}

func (b *NetworkServiceBuilder) LoadDynamicConfig() *NetworkServiceBuilder {
	if b.err != nil {
		return b
	}
	var err error

	dynamicCfg, _, err := getDynamicConfig(b.ctx, b.service.k8s)
	if err != nil {
		//serviceLog.Warnf("get dynamic config error: %s. fallback to default config", err.Error())
		dynamicCfg = ""
	}
	config, err := daemon.GetConfigFromFileWithMerge(b.configFilePath, []byte(dynamicCfg))
	if err != nil {
		b.err = fmt.Errorf("failed parse config: %v", err)
		return b
	}
	config.Populate()
	err = config.Validate()
	if err != nil {
		b.err = err
		return b
	}
	serviceLog.Info("got config", "config", fmt.Sprintf("%+v", config))

	b.config = config

	return b
}

func (b *NetworkServiceBuilder) setupAliyunClient() error {
	if os.Getenv("TERWAY_DEPLOY_ENV") == envEFLO {
		instance.SetPopulateFunc(instance.EfloPopulate)
	}
	meta := instance.GetInstanceMeta()

	var providers []credential.Interface
	if string(b.config.AccessID) != "" && string(b.config.AccessSecret) != "" {
		providers = append(providers, credential.NewAKPairProvider(string(b.config.AccessID), string(b.config.AccessSecret)))
	}
	providers = append(providers, credential.NewEncryptedCredentialProvider(utils.NormalizePath(b.config.CredentialPath), "", ""))
	providers = append(providers, credential.NewMetadataProvider())

	clientSet, err := credential.NewClientMgr(meta.RegionID, providers...)
	if err != nil {
		return err
	}

	aliyunClient, err := client.New(clientSet, client.FromMap(b.config.RateLimit))
	if err != nil {
		return err
	}
	b.aliyunClient = aliyunClient

	return nil
}

func (b *NetworkServiceBuilder) initInstanceLimit() error {
	node := b.service.k8s.Node()
	if node == nil {
		return fmt.Errorf("k8s node not found")
	}
	provider := client.LimitProviders["ecs"]
	if os.Getenv("TERWAY_DEPLOY_ENV") == envEFLO {
		provider = client.LimitProviders["eflo"]
		limit, err := provider.GetLimitFromAnno(node.Annotations)
		if err != nil {
			return err
		}
		if limit == nil {
			limit, err = provider.GetLimit(b.aliyunClient, instance.GetInstanceMeta().InstanceID)
			if err != nil {
				return fmt.Errorf("upable get instance limit, %w", err)
			}
		}
		b.limit = limit
	} else {
		limit, err := provider.GetLimitFromAnno(node.Annotations)
		if err != nil {
			return err
		}
		if limit == nil || instance.GetInstanceMeta().InstanceType != limit.InstanceTypeID {
			limit, err = provider.GetLimit(b.aliyunClient, instance.GetInstanceMeta().InstanceType)
			if err != nil {
				return fmt.Errorf("upable get instance limit, %w", err)
			}
		}
		b.limit = limit
	}

	b.service.enableIPv4, b.service.enableIPv6 = checkInstance(b.limit, b.daemonMode, b.config)
	return nil
}

func (b *NetworkServiceBuilder) setupENIManager() error {
	var (
		trunkENIID      = ""
		nodeAnnotations = map[string]string{}
	)

	enableIPv4 := b.service.enableIPv4
	enableIPv6 := b.service.enableIPv6
	eniConfig := getENIConfig(b.config)
	eniConfig.EnableIPv4 = enableIPv4
	eniConfig.EnableIPv6 = enableIPv6

	// fall back to use primary eni's sg
	if len(eniConfig.SecurityGroupIDs) == 0 {
		enis, err := b.aliyunClient.DescribeNetworkInterface(b.ctx, "", nil, eniConfig.InstanceID, "Primary", "", nil)
		if err != nil {
			return err
		}
		if len(enis) == 0 {
			return fmt.Errorf("no primary eni found")
		}
		eniConfig.SecurityGroupIDs = enis[0].SecurityGroupIDs
	}

	// get pool config
	poolConfig, err := getPoolConfig(b.config, b.daemonMode, b.limit)
	if err != nil {
		return err
	}

	poolConfig.EnableIPv4 = b.service.enableIPv4
	poolConfig.EnableIPv6 = b.service.enableIPv6

	serviceLog.Info("pool config", "pool", fmt.Sprintf("%+v", poolConfig))

	vswPool, err := vswpool.NewSwitchPool(100, "10m")
	if err != nil {
		return fmt.Errorf("error init vsw pool, %w", err)
	}
	var factory factory.Factory
	if os.Getenv("TERWAY_DEPLOY_ENV") == envEFLO {
		factory = aliyun.NewEflo(b.ctx, b.aliyunClient, vswPool, eniConfig)
	} else {
		factory = aliyun.NewAliyun(b.ctx, b.aliyunClient, eni2.NewENIMetadata(enableIPv4, enableIPv6), vswPool, eniConfig)
	}

	if b.config.EnableENITrunking {
		trunkENIID, err = initTrunk(b.config, poolConfig, b.service.k8s, factory)
		if err != nil {
			return err
		}
		if trunkENIID == "" {
			serviceLog.Info("no trunk eni found, fallback to non-trunk mode")
		} else {
			nodeAnnotations[types.TrunkOn] = trunkENIID
			nodeAnnotations[string(types.MemberENIIPTypeIPs)] = strconv.Itoa(poolConfig.MaxMemberENI)
		}
	}

	nodeAnnotations[string(types.NormalIPTypeIPs)] = strconv.Itoa(poolConfig.Capacity)

	attached, err := factory.GetAttachedNetworkInterface(trunkENIID)
	if err != nil {
		return err
	}
	realRdmaCount := b.limit.ERDMARes()
	if b.config.EnableERDMA && len(attached) >= b.limit.Adapters-1-b.limit.ERDMARes() {
		attachedERdma := lo.Filter(attached, func(ni *daemon.ENI, idx int) bool { return ni.ERdma })
		if len(attachedERdma) <= 0 {
			// turn off only when no one use it
			serviceLog.Info(fmt.Sprintf("node has no enough free eni slot to attach more erdma to achieve erdma res: %d", b.limit.ERDMARes()))
			b.config.EnableERDMA = false
		}
		// reset the cap to the actual using
		realRdmaCount = min(realRdmaCount, len(attachedERdma))
	}

	if b.config.EnableERDMA {
		if b.daemonMode == daemon.ModeENIMultiIP {
			nodeAnnotations[string(types.NormalIPTypeIPs)] = strconv.Itoa(poolConfig.Capacity - realRdmaCount*b.limit.IPv4PerAdapter)
			nodeAnnotations[string(types.ERDMAIPTypeIPs)] = strconv.Itoa(realRdmaCount * b.limit.IPv4PerAdapter)
			poolConfig.ERdmaCapacity = realRdmaCount * b.limit.IPv4PerAdapter
		} else if b.daemonMode == daemon.ModeENIOnly {
			nodeAnnotations[string(types.NormalIPTypeIPs)] = strconv.Itoa(poolConfig.Capacity - realRdmaCount)
			nodeAnnotations[string(types.ERDMAIPTypeIPs)] = strconv.Itoa(realRdmaCount)
			poolConfig.ERdmaCapacity = realRdmaCount
		}
	}

	runDevicePlugin(b.daemonMode, b.config, poolConfig)

	// ensure node annotations
	err = b.service.k8s.PatchNodeAnnotations(nodeAnnotations)
	if err != nil {
		return fmt.Errorf("error patch node annotations, %w", err)
	}
	objList, err := b.service.resourceDB.List()
	if err != nil {
		return err
	}

	attachedENIID := lo.SliceToMap(attached, func(item *daemon.ENI) (string, *daemon.ENI) {
		return item.ID, item
	})
	podResources := getPodResources(objList)
	serviceLog.Info(fmt.Sprintf("loaded pod res, %v", podResources))

	podResources = filterENINotFound(podResources, attachedENIID)

	err = preStartResourceManager(b.daemonMode, b.service.k8s)
	if err != nil {
		return err
	}

	var eniList []eni.NetworkInterface

	var (
		normalENICount int
		erdmaENICount  int
	)
	for _, ni := range attached {
		serviceLog.V(5).Info("found attached eni", "eni", ni)
		if b.config.EnableENITrunking && ni.Trunk && trunkENIID == ni.ID {
			lo := eni.NewLocal(ni, "trunk", factory, poolConfig)
			eniList = append(eniList, eni.NewTrunk(b.service.k8s.GetClient(), lo))
		} else if b.config.EnableERDMA && ni.ERdma {
			erdmaENICount++
			eniList = append(eniList, eni.NewLocal(ni, "erdma", factory, poolConfig))
		} else {
			normalENICount++
			eniList = append(eniList, eni.NewLocal(ni, "secondary", factory, poolConfig))
		}
	}
	normalENINeeded := poolConfig.MaxENI - normalENICount
	if b.config.EnableERDMA {
		normalENINeeded = poolConfig.MaxENI - b.limit.ERDMARes() - normalENICount
		for i := 0; i < b.limit.ERDMARes()-erdmaENICount; i++ {
			eniList = append(eniList, eni.NewLocal(nil, "erdma", factory, poolConfig))
		}
	}

	for i := 0; i < normalENINeeded; i++ {
		eniList = append(eniList, eni.NewLocal(nil, "secondary", factory, poolConfig))
	}

	eniManager := eni.NewManager(poolConfig.MinPoolSize, poolConfig.MaxPoolSize, poolConfig.Capacity, 30*time.Second, eniList, types.EniSelectionPolicy(b.config.EniSelectionPolicy), b.service.k8s)
	b.service.eniMgr = eniManager
	err = eniManager.Run(b.ctx, &b.service.wg, podResources)
	if err != nil {
		return err
	}

	if b.config.IPAMType != types.IPAMTypeCRD {
		//start gc loop
		go b.service.startGarbageCollectionLoop(b.ctx)
	}
	return nil
}

func (b *NetworkServiceBuilder) PostInitForLegacyMode() *NetworkServiceBuilder {
	if b.err != nil {
		return b
	}
	backoff.OverrideBackoff(b.config.BackoffOverride)
	_ = b.service.k8s.SetCustomStatefulWorkloadKinds(b.config.CustomStatefulWorkloadKinds)

	if err := b.setupAliyunClient(); err != nil {
		b.err = err
		return b
	}

	if err := b.initInstanceLimit(); err != nil {
		b.err = err
		return b
	}

	if err := b.setupENIManager(); err != nil {
		b.err = err
		return b
	}

	return b
}

func (b *NetworkServiceBuilder) PostInitForCRDV2() *NetworkServiceBuilder {
	if b.err != nil {
		return b
	}
	crdv2 := eni.NewCRDV2(b.service.k8s.NodeName())
	mgr := eni.NewManager(0, 0, 0, 0, []eni.NetworkInterface{crdv2}, types.EniSelectionPolicy(b.config.EniSelectionPolicy), nil)

	svc := b.RunENIMgr(b.ctx, mgr)
	go b.service.startGarbageCollectionLoop(b.ctx)

	return svc
}

func (b *NetworkServiceBuilder) InitResourceDB() *NetworkServiceBuilder {
	if b.err != nil {
		return b
	}
	var err error
	b.service.resourceDB, err = storage.NewDiskStorage(
		resDBName, utils.NormalizePath(resDBPath), json.Marshal, func(bytes []byte) (interface{}, error) {
			resourceRel := &daemon.PodResources{}
			err = json.Unmarshal(bytes, resourceRel)
			if err != nil {
				return nil, err
			}
			return *resourceRel, nil
		})
	if err != nil {
		b.err = err
		return b
	}
	return b
}

func (b *NetworkServiceBuilder) RunENIMgr(ctx context.Context, mgr *eni.Manager) *NetworkServiceBuilder {
	if b.err != nil {
		return b
	}
	b.service.eniMgr = mgr
	err := b.service.eniMgr.Run(ctx, &b.service.wg, nil)
	if err != nil {
		b.err = err
		return b
	}
	return b
}

func (b *NetworkServiceBuilder) RegisterTracing() *NetworkServiceBuilder {
	if b.err != nil {
		return b
	}
	_ = tracing.Register(tracing.ResourceTypeNetworkService, "default", b.service)
	tracing.RegisterResourceMapping(b.service)
	tracing.RegisterEventRecorder(b.service.k8s.RecordNodeEvent, b.service.k8s.RecordPodEvent)
	return b
}

func (b *NetworkServiceBuilder) Build() (*networkService, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.service, nil
}

func newCRDV2Service(ctx context.Context, configFilePath, daemonMode string) (*networkService, error) {
	builder := NewNetworkServiceBuilder(ctx).
		WithConfigFilePath(configFilePath).
		WithDaemonMode(daemonMode).
		InitService().
		LoadGlobalConfig().
		InitK8S().
		InitResourceDB().
		PostInitForCRDV2().
		RegisterTracing()

	return builder.Build()
}

func newLegacyService(ctx context.Context, configFilePath, daemonMode string) (*networkService, error) {
	builder := NewNetworkServiceBuilder(ctx).
		WithConfigFilePath(configFilePath).
		WithDaemonMode(daemonMode).
		InitService().
		LoadGlobalConfig().
		InitK8S().
		InitResourceDB().
		LoadDynamicConfig().
		PostInitForLegacyMode().
		RegisterTracing()

	return builder.Build()
}
