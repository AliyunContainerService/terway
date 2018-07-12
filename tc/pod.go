package tc

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"gitlab.alibaba-inc.com/cos/terway/idm"
	"gitlab.alibaba-inc.com/cos/terway/types"
	"net"
	"regexp"
	"strconv"
)

var BANDWIDTH_REGEXP *regexp.Regexp

func init() {
	BANDWIDTH_REGEXP = regexp.MustCompile("(?i)^(?P<bandwidth>\\d+)(?P<unit>m)?$")
}

func BuildRule(podIP net.IP, id string, bandwidth string) *types.TrafficShappingRule {

	match := BANDWIDTH_REGEXP.FindStringSubmatch(bandwidth)
	if len(match) == 0 {
		log.Warnf("pod %s: %s is not valid bandwidth", id, bandwidth)
		return nil
	}
	bw, _ := strconv.Atoi(match[1])
	ip := podIP.String()
	return &types.TrafficShappingRule{
		ID:        id,
		Source:    ip,
		Bandwidth: fmt.Sprintf("%dmbps", bw),
	}
}

func Addrule(rule *types.TrafficShappingRule, tc TC) {
	if rule == nil {
		return
	}
	if rule.Source == "" {
		log.Warnf("ip address of %s is empty", rule.ID)
		return
	}

	log.Infof("try add rule for %s, bandwidth: %s", rule.ID, rule.Bandwidth)

	rule.Classify = idm.AcquireID(rule.ID)
	if err := tc.AddRule(rule); err != nil {
		log.Errorf("pod %s: failed to add rule", rule.ID)
		return
	}

}

func Delrule(rule *types.TrafficShappingRule, tc TC) {
	if rule == nil {
		return
	}
	classify := idm.GetId(rule.ID)
	if classify < 0 {
		log.Warnf("pod %s: not is in our records", rule.ID)
		return
	}
	rule.Classify = classify
	log.Infof("try delete rule for %s", rule.ID)
	if err := tc.DeleteRule(rule); err != nil {
		log.Errorf("pod %s: failed to delete rule", rule.ID)
		return
	}
	idm.ReleaseID(rule.ID)
}

func GenerateRuleID(direction, podID string) string {
	return fmt.Sprintf("%s.%s", direction, podID)
}
