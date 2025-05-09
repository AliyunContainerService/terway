/*
Copyright 2021 Terway Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
// Code generated by informer-gen. DO NOT EDIT.

package v1beta1

import (
	"context"
	time "time"

	networkalibabacloudcomv1beta1 "github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	versioned "github.com/AliyunContainerService/terway/pkg/generated/clientset/versioned"
	internalinterfaces "github.com/AliyunContainerService/terway/pkg/generated/informers/externalversions/internalinterfaces"
	v1beta1 "github.com/AliyunContainerService/terway/pkg/generated/listers/network.alibabacloud.com/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// NodeRuntimeInformer provides access to a shared informer and lister for
// NodeRuntimes.
type NodeRuntimeInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1beta1.NodeRuntimeLister
}

type nodeRuntimeInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
}

// NewNodeRuntimeInformer constructs a new informer for NodeRuntime type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewNodeRuntimeInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredNodeRuntimeInformer(client, resyncPeriod, indexers, nil)
}

// NewFilteredNodeRuntimeInformer constructs a new informer for NodeRuntime type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredNodeRuntimeInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.NetworkV1beta1().NodeRuntimes().List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.NetworkV1beta1().NodeRuntimes().Watch(context.TODO(), options)
			},
		},
		&networkalibabacloudcomv1beta1.NodeRuntime{},
		resyncPeriod,
		indexers,
	)
}

func (f *nodeRuntimeInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredNodeRuntimeInformer(client, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *nodeRuntimeInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&networkalibabacloudcomv1beta1.NodeRuntime{}, f.defaultInformer)
}

func (f *nodeRuntimeInformer) Lister() v1beta1.NodeRuntimeLister {
	return v1beta1.NewNodeRuntimeLister(f.Informer().GetIndexer())
}
