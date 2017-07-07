package schedulercache

import (
	"k8s.io/kubernetes/pkg/api/v1"
)

type NamespaceInfo struct {
	namespace *v1.Namespace

	allocatedPods []*v1.Pod

	pendingPods []*v1.Pod

	requestedResource *Resource

	allocatedResource *Resource

	// Whenever NamespaceInfo changes, generation is bumped.
	// This is used to avoid cloning it if the object didn't change.
	generation int64
}

func (n *NamespaceInfo) score() {

}

// NewNamespaceInfo returns a ready to use empty NamespaceInfo object.
// If any pods are given in arguments, their information will be aggregated in
// the returned object.
func NewNamespaceInfo(pods ...*v1.Pod) *NamespaceInfo {
	ni := &NamespaceInfo{
		allocatedResource: &Resource{},
	}

	return ni
}

func (n *NamespaceInfo) AddPod(pod *v1.Pod) {

	res, _, _ := calculateResource(pod)
	n.requestedResource.MilliCPU += res.MilliCPU
	n.requestedResource.Memory += res.Memory
	n.requestedResource.NvidiaGPU += res.NvidiaGPU
	if n.requestedResource.OpaqueIntResources == nil && len(res.OpaqueIntResources) > 0 {
		n.requestedResource.OpaqueIntResources = map[v1.ResourceName]int64{}
	}
	for rName, rQuant := range res.OpaqueIntResources {
		n.requestedResource.OpaqueIntResources[rName] += rQuant
	}

	if pod.Spec.NodeName == "" {
		n.pendingPods = append(n.pendingPods, pod)
	} else {
		n.allocatedPods = append(n.allocatedPods, pod)
	}

	n.generation++
}

func (n *NamespaceInfo) RemovePod(pod *v1.Pod) error {
	return nil
}
