package client

import (
	"github.com/AliyunContainerService/terway/pkg/aliyun/credential"
	"go.opentelemetry.io/otel/trace"
)

var _ ECS = &ECSService{}

type ECSService struct {
	ClientSet        credential.Client
	IdempotentKeyGen IdempotentKeyGen
	RateLimiter      *RateLimiter
	Tracer           trace.Tracer
}

func NewECSService(clientSet credential.Client, rateLimiter *RateLimiter, tracer trace.Tracer) *ECSService {
	return &ECSService{
		ClientSet:        clientSet,
		IdempotentKeyGen: NewIdempotentKeyGenerator(),
		RateLimiter:      rateLimiter,
		Tracer:           tracer,
	}
}
