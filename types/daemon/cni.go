package daemon

type CNI struct {
	PodName      string
	PodNamespace string
	PodID        string
	PodUID       string
	NetNSPath    string
}
