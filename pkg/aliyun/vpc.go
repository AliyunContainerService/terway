package aliyun

import (
	"context"
	"fmt"
	"time"

	apiErr "github.com/AliyunContainerService/terway/pkg/aliyun/errors"
	"github.com/AliyunContainerService/terway/pkg/metric"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

// DescribeVSwitchByID get vsw by id
func (a *OpenAPI) DescribeVSwitchByID(ctx context.Context, vSwitch string) (*vpc.VSwitch, error) {
	req := vpc.CreateDescribeVSwitchesRequest()
	req.VSwitchId = vSwitch

	l := log.WithFields(map[string]interface{}{
		LogFieldAPI:       "DescribeVSwitches",
		LogFieldVSwitchID: vSwitch,
	})

	start := time.Now()
	resp, err := a.ClientSet.VPC().DescribeVSwitches(req)
	metric.OpenAPILatency.WithLabelValues("DescribeVSwitches", fmt.Sprint(err != nil)).Observe(metric.MsSince(start))
	if err != nil {
		l.WithField(LogFieldRequestID, apiErr.ErrRequestID(err)).Error(err)
		return nil, err
	}
	l.WithField(LogFieldRequestID, resp.RequestId).Debugf("DescribeVSwitches: vsw slice = %+v, err = %v", resp.VSwitches.VSwitch, err)
	if len(resp.VSwitches.VSwitch) == 0 {
		return nil, apiErr.ErrNotFound
	}
	if len(resp.VSwitches.VSwitch) > 0 {
		return &resp.VSwitches.VSwitch[0], nil
	}
	return nil, err
}
