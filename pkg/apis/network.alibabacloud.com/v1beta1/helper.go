package v1beta1

func (p *PodENISpec) HaveFixedIP() bool {
	for _, a := range p.Allocations {
		if a.AllocationType.Type == IPAllocTypeFixed {
			return true
		}
	}
	return false
}
