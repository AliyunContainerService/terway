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

package common

import (
	"context"

	"github.com/AliyunContainerService/terway/pkg/apis/network.alibabacloud.com/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// UpdatePodENI update cr
func UpdatePodENI(ctx context.Context, c client.Client, update *v1beta1.PodENI) (*v1beta1.PodENI, error) {
	var err error
	for i := 0; i < 2; i++ {
		err = c.Update(ctx, update)
		if err != nil {
			continue
		}
		return update, nil
	}
	return nil, err
}

// UpdatePodENIStatus set cr status
func UpdatePodENIStatus(ctx context.Context, c client.Client, update *v1beta1.PodENI) (*v1beta1.PodENI, error) {
	var err error
	for i := 0; i < 2; i++ {
		err = c.Status().Update(ctx, update)
		if err != nil {
			continue
		}
		return update, nil
	}
	return nil, err
}
