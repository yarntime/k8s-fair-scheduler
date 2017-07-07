package schedulercache

import (
	priorityutil "k8s-fair-scheduler/pkg/scheduler/algorithm/priorities/util"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/kubernetes/pkg/api/v1"
	v1helper "k8s.io/kubernetes/pkg/api/v1/helper"
)

// Resource is a collection of compute resource.
type Resource struct {
	MilliCPU           int64
	Memory             int64
	NvidiaGPU          int64
	OpaqueIntResources map[v1.ResourceName]int64
}

func (r *Resource) ResourceList() v1.ResourceList {
	result := v1.ResourceList{
		v1.ResourceCPU:       *resource.NewMilliQuantity(r.MilliCPU, resource.DecimalSI),
		v1.ResourceMemory:    *resource.NewQuantity(r.Memory, resource.BinarySI),
		v1.ResourceNvidiaGPU: *resource.NewQuantity(r.NvidiaGPU, resource.DecimalSI),
	}
	for rName, rQuant := range r.OpaqueIntResources {
		result[rName] = *resource.NewQuantity(rQuant, resource.DecimalSI)
	}
	return result
}

func (r *Resource) Clone() *Resource {
	res := &Resource{
		MilliCPU:  r.MilliCPU,
		Memory:    r.Memory,
		NvidiaGPU: r.NvidiaGPU,
	}
	res.OpaqueIntResources = make(map[v1.ResourceName]int64)
	for k, v := range r.OpaqueIntResources {
		res.OpaqueIntResources[k] = v
	}
	return res
}

func (r *Resource) AddOpaque(name v1.ResourceName, quantity int64) {
	r.SetOpaque(name, r.OpaqueIntResources[name]+quantity)
}

func (r *Resource) SetOpaque(name v1.ResourceName, quantity int64) {
	// Lazily allocate opaque integer resource map.
	if r.OpaqueIntResources == nil {
		r.OpaqueIntResources = map[v1.ResourceName]int64{}
	}
	r.OpaqueIntResources[name] = quantity
}

func CalculateResource(pod *v1.Pod) (res Resource, non0_cpu int64, non0_mem int64) {
	for _, c := range pod.Spec.Containers {
		for rName, rQuant := range c.Resources.Requests {
			switch rName {
			case v1.ResourceCPU:
				res.MilliCPU += rQuant.MilliValue()
			case v1.ResourceMemory:
				res.Memory += rQuant.Value()
			case v1.ResourceNvidiaGPU:
				res.NvidiaGPU += rQuant.Value()
			default:
				if v1helper.IsOpaqueIntResourceName(rName) {
					res.AddOpaque(rName, rQuant.Value())
				}
			}
		}

		non0_cpu_req, non0_mem_req := priorityutil.GetNonzeroRequests(&c.Resources.Requests)
		non0_cpu += non0_cpu_req
		non0_mem += non0_mem_req
		// No non-zero resources for GPUs or opaque resources.
	}
	return
}
