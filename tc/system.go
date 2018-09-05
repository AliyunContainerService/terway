package tc

import (
	log "github.com/sirupsen/logrus"
	"os/exec"

	"fmt"
	"github.com/AliyunContainerService/terway/types"
	"os"
)

const (
	DIRECTION_INGRESS = "ingress"
	DIRECTION_EGRESS  = "egress"
)

type TC interface {
	AddRule(rule *types.TrafficShappingRule) error
	DeleteRule(rule *types.TrafficShappingRule) error
}

type System struct {
	Interface string
	Direction string
}

func NewNull() TC {
	return &NullTC{}
}

type NullTC struct {
}

func (n *NullTC) AddRule(rule *types.TrafficShappingRule) error {
	return nil
}

func (n *NullTC) DeleteRule(rule *types.TrafficShappingRule) error {
	return nil
}

func NewSystem(iface string, direction string) (TC, error) {
	s := &System{
		Interface: iface,
		Direction: direction,
	}
	if err := s.run("init", nil); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *System) initialize(refresh bool) error {
	return s.run("init", nil)
}

func (s *System) AddRule(rule *types.TrafficShappingRule) error {
	return s.run("add", rule)
}

func (s *System) DeleteRule(rule *types.TrafficShappingRule) error {
	return s.run("del", rule)
}

func (s *System) run(action string, rule *types.TrafficShappingRule) error {
	cmd := exec.Command("traffic", action)
	env := os.Environ()
	if rule != nil {
		env = append(env, s.buildEnviron(rule)...)
	}
	env = append(env, "INTERFACE="+s.Interface)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	log.Debugf("running env: %+v result: %v", env, string(output))
	if err != nil {
		//TODO 放在Error里返回
		log.Errorf("exec traffic: %v. output: \n %s", err, output)
	}
	return err
}

func (s *System) buildEnviron(rule *types.TrafficShappingRule) []string {
	environ := map[string]interface{}{
		"ID":        rule.ID,
		"CLASSIFY":  fmt.Sprintf("%x", rule.Classify),
		"BANDWIDTH": rule.Bandwidth,
		"SOURCE":    rule.Source,
		"DIRECTION": s.Direction,
	}

	ret := make([]string, 0, len(environ))
	for k, v := range environ {
		ret = append(ret, fmt.Sprintf("%s=%v", k, v))

	}
	return ret
}
