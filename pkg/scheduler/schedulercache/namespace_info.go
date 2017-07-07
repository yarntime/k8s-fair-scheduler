package schedulercache

import (
	"k8s.io/kubernetes/pkg/api/v1"
)


type NamespaceInfo struct {

	namespace *v1.Namespace

	allocatedPods             []*v1.Pod

	pendingPods               []*v1.Pod

	allocatedResource *Resource
}

func (n *NamespaceInfo) score() {

}


// NewNamespaceInfo returns a ready to use empty NamespaceInfo object.
// If any pods are given in arguments, their information will be aggregated in
// the returned object.
func NewNamespaceInfo(pods ...*v1.Pod) *NamespaceInfo {
	ni := &NamespaceInfo{
		allocatedResource:   &Resource{},
	}

	return ni
}