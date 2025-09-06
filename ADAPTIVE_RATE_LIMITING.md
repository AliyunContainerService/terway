# Adaptive Rate Limiting Implementation

这个实现基于 AWS SDK Go v2 的自适应限流算法，为 terway 项目提供了智能的 API 限流功能。

## 概述

自适应限流器使用 Cubic 算法动态调整请求速率，根据 API 响应自动适应阿里云的限流情况：

- **成功时**：逐步增加请求速率
- **限流时**：立即降低请求速率
- **恢复时**：按照 Cubic 曲线平滑恢复

## 主要特性

1. **向后兼容**：保持与现有 `RateLimiter` 相同的 `Wait()` 接口
2. **自适应调整**：根据 API 响应自动调整限流速率
3. **智能错误识别**：自动识别阿里云的限流错误模式
4. **并发安全**：所有操作都是线程安全的
5. **上下文支持**：支持 context 取消

## 核心组件

### 1. AdaptiveRateLimiter
主要的自适应限流器实现。

```go
type AdaptiveRateLimiter struct {
    store map[string]*adaptiveRateLimit
}
```

### 2. AdaptiveRateLimiterWrapper
提供便捷的包装器，自动处理 API 响应。

```go
type AdaptiveRateLimiterWrapper struct {
    limiter *AdaptiveRateLimiter
}
```

### 3. adaptiveRateLimit
单个 API 的自适应限流器，实现 Cubic 算法。

## 使用方法

### 方法一：直接替换（最小修改）

```go
// 原来的代码
rateLimiter := NewRateLimiter(cfg)

// 替换为
rateLimiter := NewAdaptiveRateLimiter(cfg)

// 其他代码保持不变
err := rateLimiter.Wait(ctx, "DescribeNetworkInterfaces")
```

### 方法二：添加响应处理（推荐）

```go
rateLimiter := NewAdaptiveRateLimiter(cfg)

// API 调用前等待
err := rateLimiter.Wait(ctx, "DescribeNetworkInterfaces")
if err != nil {
    return err
}

// 执行 API 调用
result, apiErr := client.DescribeNetworkInterfaces(...)

// 处理响应以启用自适应行为
rateLimiter.HandleResponse("DescribeNetworkInterfaces", apiErr)

return apiErr
```

### 方法三：使用包装器（最简洁）

```go
wrapper := NewAdaptiveRateLimiterWrapper(cfg)

err := wrapper.WaitAndHandle(ctx, "DescribeNetworkInterfaces", func() error {
    _, err := client.DescribeNetworkInterfaces(...)
    return err
})
```

## 迁移策略

### 1. 渐进式迁移

```go
func createRateLimiter(useAdaptive bool) RateLimiterInterface {
    if useAdaptive {
        return NewAdaptiveRateLimiter(cfg)
    }
    return NewRateLimiter(cfg)
}
```

### 2. 服务级别迁移

对于每个服务（ECS, VPC, EFLO），可以独立选择是否启用自适应限流：

```go
type ECSService struct {
    RateLimiter RateLimiterInterface  // 使用接口
    // ... 其他字段
}

// 在服务初始化时选择实现
func NewECSService(useAdaptive bool) *ECSService {
    var rateLimiter RateLimiterInterface
    if useAdaptive {
        rateLimiter = NewAdaptiveRateLimiter(cfg)
    } else {
        rateLimiter = NewRateLimiter(cfg)
    }
    
    return &ECSService{
        RateLimiter: rateLimiter,
    }
}
```

## 错误识别

自适应限流器自动识别以下阿里云限流错误模式：

- `Throttling`
- `RequestThrottled`
- `TooManyRequests`
- `QPS Limit Exceeded`
- `Bandwidth.Out.Limit.Exceeded`
- `Request was denied due to request throttling`

## 算法原理

### Cubic 算法
基于 TCP Cubic 拥塞控制算法：

1. **正常状态**：按照 Cubic 函数增长速率
2. **限流检测**：立即将速率降低到 β × 当前速率（β = 0.7）
3. **恢复阶段**：使用 Cubic 函数平滑恢复到之前的最大速率

### 参数配置
- `smooth = 0.8`：平滑因子，用于计算测量的传输速率
- `beta = 0.7`：限流时的速率衰减因子
- `scaleConstant = 0.4`：Cubic 函数的比例常数
- `minFillRate = 0.5`：最小填充速率

## 性能影响

1. **内存开销**：每个 API 增加约 200 字节的状态信息
2. **CPU 开销**：每次 API 调用增加约 1-2 微秒的计算时间
3. **延迟影响**：在限流情况下可能增加等待时间，但能减少被服务端拒绝的概率

## 监控和调试

自适应限流器复用现有的监控指标：

```go
metric.RateLimiterLatency.WithLabelValues(name).Observe(float64(took.Milliseconds()))
```

在日志中会显示 "adaptive rate limit" 标识：

```
INFO adaptive rate limit api=DescribeNetworkInterfaces took=1.234
```

## 配置建议

### 初始配置
保持现有的 QPS 和 Burst 配置作为初始值：

```go
cfg := LimitConfig{
    "DescribeNetworkInterfaces": {QPS: 13.33, Burst: 800},  // 800/min
    "CreateNetworkInterface":    {QPS: 8.33,  Burst: 500},  // 500/min
}
```

### 生产环境部署
1. 先在测试环境验证
2. 使用特性开关控制启用
3. 监控错误率和延迟变化
4. 逐步扩展到所有 API

## 注意事项

1. **响应处理**：必须调用 `HandleResponse()` 才能启用自适应行为
2. **错误识别**：确保错误模式匹配阿里云的实际响应
3. **上下文取消**：长时间等待可以通过 context 取消
4. **并发安全**：所有方法都是线程安全的，可以并发调用

## 测试

运行测试验证实现：

```bash
go test ./pkg/aliyun/client -v -run TestAdaptive
```

## 未来扩展

1. **配置化参数**：允许调整 Cubic 算法参数
2. **监控指标**：添加自适应限流专用的监控指标
3. **预测性限流**：基于历史数据预测限流情况
4. **多级限流**：支持全局和单 API 的多级限流策略