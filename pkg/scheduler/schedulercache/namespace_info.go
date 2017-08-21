package schedulercache

import (
	"github.com/foize/go.fifo"
	"github.com/golang/glog"
	"k8s.io/kubernetes/pkg/api/v1"
	"math"
)

var (
	SYSTEM_NAMESPACE       = "kube-system"
	MAX_SCORE        int32 = 100
)

type NamespaceInfo struct {
	namespace *v1.Namespace

	pendingPods *fifo.Queue

	requestedResource *Resource

	allocatedResource *Resource

	// Whenever NamespaceInfo changes, generation is bumped.
	// This is used to avoid cloning it if the object didn't change.
	generation int64
}

func (n *NamespaceInfo) score(totalResource *Resource) int32 {

	if n.namespace != nil {
		glog.V(5).Infof("current namespace is %s\n", n.namespace.Name)
		if n.namespace.Name == SYSTEM_NAMESPACE {
			return MAX_SCORE * 2
		}

		if n.namespace.ObjectMeta.Annotations != nil {
			if _, ok := n.namespace.ObjectMeta.Annotations[SYSTEM_NAMESPACE]; ok {
				return MAX_SCORE + MAX_SCORE/2
			}
		}
	}

	glog.V(5).Infof("%f, %f, %f, %f\n", float64(n.allocatedResource.Memory), float64(totalResource.Memory), float64(n.allocatedResource.MilliCPU), float64(totalResource.MilliCPU))
	memRatio, cpuRatio := float64(n.allocatedResource.Memory)/float64(totalResource.Memory), float64(n.allocatedResource.MilliCPU)/float64(totalResource.MilliCPU)

	return int32(100) - int32(math.Max(memRatio, cpuRatio)*100)
}

// NewNamespaceInfo returns a ready to use empty NamespaceInfo object.
// If any pods are given in arguments, their information will be aggregated in
// the returned object.
func NewNamespaceInfo(namespace *v1.Namespace) *NamespaceInfo {
	ni := &NamespaceInfo{
		requestedResource: &Resource{},
		allocatedResource: &Resource{},
		pendingPods:       fifo.NewQueue(),
	}

	return ni
}

func (n *NamespaceInfo) AddPodToQueue(pod *v1.Pod) {

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

	n.pendingPods.Add(pod)

	n.generation++
}

func (n *NamespaceInfo) AddPod(pod *v1.Pod) {
	res, _, _ := calculateResource(pod)

	n.allocatedResource.MilliCPU += res.MilliCPU
	n.allocatedResource.Memory += res.Memory
	n.allocatedResource.NvidiaGPU += res.NvidiaGPU
	if n.allocatedResource.OpaqueIntResources == nil && len(res.OpaqueIntResources) > 0 {
		n.allocatedResource.OpaqueIntResources = map[v1.ResourceName]int64{}
	}
	for rName, rQuant := range res.OpaqueIntResources {
		n.allocatedResource.OpaqueIntResources[rName] += rQuant
	}

	n.generation++
}

func (n *NamespaceInfo) RemovePod(pod *v1.Pod) error {

	glog.V(5).Infof("release resource of pod %s/%s\n", pod.Namespace, pod.Name)

	res, _, _ := calculateResource(pod)

	n.requestedResource.MilliCPU -= res.MilliCPU
	n.requestedResource.Memory -= res.Memory
	n.requestedResource.NvidiaGPU -= res.NvidiaGPU

	if pod.Spec.NodeName != "" {
		n.allocatedResource.MilliCPU -= res.MilliCPU
		n.allocatedResource.Memory -= res.Memory
		n.allocatedResource.NvidiaGPU -= res.NvidiaGPU
	}

	return nil
}

// use FIFO queue now, we may change this later.
func (n *NamespaceInfo) GetNextPod() (*v1.Pod, error) {

	p := n.pendingPods.Next().(*v1.Pod)

	return p, nil
}
