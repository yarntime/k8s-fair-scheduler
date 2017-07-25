/*
Copyright 2015 The Kubernetes Authors.

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

package schedulercache

import (
	"fmt"
	"sync"
	"time"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/kubernetes/pkg/api/v1"
	"math"
)

var (
	cleanAssumedPeriod = 1 * time.Second
)

// New returns a Cache implementation.
// It automatically starts a go routine that manages expiration of assumed pods.
// "ttl" is how long the assumed pod will get expired.
// "stop" is the channel that would close the background goroutine.
func New(ttl time.Duration, stop <-chan struct{}) Cache {
	cache := newSchedulerCache(ttl, cleanAssumedPeriod, stop)
	cache.run()
	return cache
}

type schedulerCache struct {
	stop   <-chan struct{}
	ttl    time.Duration
	period time.Duration

	// This mutex guards all fields within this cache struct.
	mu sync.Mutex

	totalResource *Resource
	// a set of assumed pod keys.
	// The key could further be used to get an entry in podStates.
	assumedPods map[string]bool
	// a map from pod key to podState.
	podStates  map[string]*podState
	nodes      map[string]*NodeInfo
	namespaces map[string]*NamespaceInfo
}

type podState struct {
	pod *v1.Pod
	// Used by assumedPod to determinate expiration.
	deadline *time.Time
	// Used to block cache from expiring assumedPod if binding still runs
	bindingFinished bool
}

func newSchedulerCache(ttl, period time.Duration, stop <-chan struct{}) *schedulerCache {
	return &schedulerCache{
		ttl:    ttl,
		period: period,
		stop:   stop,

		totalResource: &Resource{},
		nodes:         make(map[string]*NodeInfo),
		assumedPods:   make(map[string]bool),
		podStates:     make(map[string]*podState),
		namespaces:    make(map[string]*NamespaceInfo),
	}
}

func (cache *schedulerCache) UpdateNodeNameToInfoMap(nodeNameToInfo map[string]*NodeInfo) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for name, info := range cache.nodes {
		if current, ok := nodeNameToInfo[name]; !ok || current.generation != info.generation {
			nodeNameToInfo[name] = info.Clone()
		}
	}
	for name := range nodeNameToInfo {
		if _, ok := cache.nodes[name]; !ok {
			delete(nodeNameToInfo, name)
		}
	}
	return nil
}

func (cache *schedulerCache) List(selector labels.Selector) ([]*v1.Pod, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	var pods []*v1.Pod
	for _, info := range cache.nodes {
		for _, pod := range info.pods {
			if selector.Matches(labels.Set(pod.Labels)) {
				pods = append(pods, pod)
			}
		}
	}
	return pods, nil
}

func (cache *schedulerCache) AssumePod(pod *v1.Pod) error {
	key, err := getPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.podStates[key]; ok {
		return fmt.Errorf("pod %v state wasn't initial but get assumed", key)
	}

	cache.addPod(pod)
	ps := &podState{
		pod: pod,
	}
	cache.podStates[key] = ps
	cache.assumedPods[key] = true
	return nil
}

func (cache *schedulerCache) FinishBinding(pod *v1.Pod) error {
	return cache.finishBinding(pod, time.Now())
}

// finishBinding exists to make tests determinitistic by injecting now as an argument
func (cache *schedulerCache) finishBinding(pod *v1.Pod, now time.Time) error {
	key, err := getPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	glog.V(5).Infof("Finished binding for pod %v. Can be expired.", key)
	currState, ok := cache.podStates[key]
	if ok && cache.assumedPods[key] {
		dl := now.Add(cache.ttl)
		currState.bindingFinished = true
		currState.deadline = &dl
	}
	return nil
}

func (cache *schedulerCache) ForgetPod(pod *v1.Pod) error {
	key, err := getPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[key]
	if ok && currState.pod.Spec.NodeName != pod.Spec.NodeName {
		return fmt.Errorf("pod %v state was assumed on a different node", key)
	}

	switch {
	// Only assumed pod can be forgotten.
	case ok && cache.assumedPods[key]:
		err := cache.removePod(pod)
		if err != nil {
			return err
		}
		delete(cache.assumedPods, key)
		delete(cache.podStates, key)
	default:
		return fmt.Errorf("pod %v state wasn't assumed but get forgotten", key)
	}
	return nil
}

// Assumes that lock is already acquired.
func (cache *schedulerCache) addPod(pod *v1.Pod) {
	node, ok := cache.nodes[pod.Spec.NodeName]
	if !ok {
		node = NewNodeInfo()
		cache.nodes[pod.Spec.NodeName] = node
	}
	node.addPod(pod)

	namespace, ok := cache.namespaces[pod.Name]
	if !ok {
		namespace = NewNamespaceInfo()
		cache.namespaces[pod.Namespace] = namespace
	}
	namespace.AddPod(pod)
}

// Assumes that lock is already acquired.
func (cache *schedulerCache) updatePod(oldPod, newPod *v1.Pod) error {
	if err := cache.removePod(oldPod); err != nil {
		return err
	}
	cache.addPod(newPod)
	return nil
}

// Assumes that lock is already acquired.
func (cache *schedulerCache) removePod(pod *v1.Pod) error {
	node := cache.nodes[pod.Spec.NodeName]
	if err := node.removePod(pod); err != nil {
		return err
	}
	if len(node.pods) == 0 && node.node == nil {
		delete(cache.nodes, pod.Spec.NodeName)
	}

	namespace := cache.namespaces[pod.Name]
	if err := namespace.RemovePod(pod); err != nil {
		return err
	}

	return nil
}

func (cache *schedulerCache) AddPod(pod *v1.Pod) error {
	key, err := getPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[key]
	switch {
	case ok && cache.assumedPods[key]:
		if currState.pod.Spec.NodeName != pod.Spec.NodeName {
			// The pod was added to a different node than it was assumed to.
			glog.Warningf("Pod %v assumed to a different node than added to.", key)
			// Clean this up.
			cache.removePod(currState.pod)
			cache.addPod(pod)
		}
		delete(cache.assumedPods, key)
		cache.podStates[key].deadline = nil
	case !ok:
		// Pod was expired. We should add it back.
		cache.addPod(pod)
		ps := &podState{
			pod: pod,
		}
		cache.podStates[key] = ps
	default:
		return fmt.Errorf("pod was already in added state. Pod key: %v", key)
	}
	return nil
}

func (cache *schedulerCache) UpdatePod(oldPod, newPod *v1.Pod) error {
	key, err := getPodKey(oldPod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[key]
	switch {
	// An assumed pod won't have Update/Remove event. It needs to have Add event
	// before Update event, in which case the state would change from Assumed to Added.
	case ok && !cache.assumedPods[key]:
		if currState.pod.Spec.NodeName != newPod.Spec.NodeName {
			glog.Errorf("Pod %v updated on a different node than previously added to.", key)
			glog.Fatalf("Schedulercache is corrupted and can badly affect scheduling decisions")
		}
		if err := cache.updatePod(oldPod, newPod); err != nil {
			return err
		}
	default:
		return fmt.Errorf("pod %v state wasn't added but get updated", key)
	}
	return nil
}

func (cache *schedulerCache) RemovePod(pod *v1.Pod) error {
	key, err := getPodKey(pod)
	if err != nil {
		return err
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	currState, ok := cache.podStates[key]
	switch {
	// An assumed pod won't have Delete/Remove event. It needs to have Add event
	// before Remove event, in which case the state would change from Assumed to Added.
	case ok && !cache.assumedPods[key]:
		if currState.pod.Spec.NodeName != pod.Spec.NodeName {
			glog.Errorf("Pod %v removed from a different node than previously added to.", key)
			glog.Fatalf("Schedulercache is corrupted and can badly affect scheduling decisions")
		}
		err := cache.removePod(currState.pod)
		if err != nil {
			return err
		}
		delete(cache.podStates, key)
	default:
		return fmt.Errorf("pod state wasn't added but get removed. Pod key: %v", key)
	}
	return nil
}

func (cache *schedulerCache) GetNextPod() (*v1.Pod, error) {
	var maxScore int32 = math.MinInt32
	var selectedNamespaceInfo *NamespaceInfo
	for _, v := range cache.namespaces {
		if v.namespace == nil {
			glog.V(5).Infof("namespace is not added...\n")
			continue
		}
		curScore := v.score(cache.totalResource)

		glog.V(5).Infof("namespace %v's score: %d\n", v.namespace.Name, curScore)
		glog.V(5).Infof("namespace pending job number: %d\n", v.pendingPods.Len())

		if curScore > maxScore && v.pendingPods.Len() != 0 {
			maxScore = curScore
			selectedNamespaceInfo = v
		}
	}

	if selectedNamespaceInfo == nil {
		return nil, nil
	}

	return selectedNamespaceInfo.GetNextPod()
}

func (cache *schedulerCache) PushBackPod(pod *v1.Pod) error {
	namespace := pod.Namespace
	for k, v := range cache.namespaces {
		if k == namespace {
			v.pendingPods.Add(pod)
			return nil
		}
	}
	return nil
}

func (cache *schedulerCache) AddNode(node *v1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodes[node.Name]
	if !ok {
		n = NewNodeInfo()
		cache.nodes[node.Name] = n
	}

	err := n.SetNode(node)
	if err != nil {
		return err
	}

	cache.totalResource.MilliCPU += n.allocatableResource.MilliCPU
	cache.totalResource.Memory += n.allocatableResource.Memory
	cache.totalResource.NvidiaGPU += n.allocatableResource.NvidiaGPU

	return nil
}

func (cache *schedulerCache) UpdateNode(oldNode, newNode *v1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.nodes[newNode.Name]
	if !ok {
		n = NewNodeInfo()
		cache.nodes[newNode.Name] = n
	}
	return n.SetNode(newNode)
}

func (cache *schedulerCache) RemoveNode(node *v1.Node) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n := cache.nodes[node.Name]
	if err := n.RemoveNode(node); err != nil {
		return err
	}
	// We remove NodeInfo for this node only if there aren't any pods on this node.
	// We can't do it unconditionally, because notifications about pods are delivered
	// in a different watch, and thus can potentially be observed later, even though
	// they happened before node removal.
	if len(n.pods) == 0 && n.node == nil {
		delete(cache.nodes, node.Name)
	}
	return nil
}

func (cache *schedulerCache) AddNamespace(namespace *v1.Namespace) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	n, ok := cache.namespaces[namespace.Name]
	if !ok {
		n = NewNamespaceInfo()
		cache.namespaces[namespace.Name] = n
	}

	if n.namespace == nil {
		n.namespace = namespace
		fmt.Printf("adding namespace %s\n", namespace.Name)
	}
	return nil
}

// RemoveNamespace deletes namespace from cache
func (cache *schedulerCache) RemoveNamespace(namespace *v1.Namespace) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	delete(cache.namespaces, namespace.Name)

	return nil
}

func (cache *schedulerCache) run() {
	go wait.Until(cache.cleanupExpiredAssumedPods, cache.period, cache.stop)
}

func (cache *schedulerCache) cleanupExpiredAssumedPods() {
	cache.cleanupAssumedPods(time.Now())
}

// cleanupAssumedPods exists for making test deterministic by taking time as input argument.
func (cache *schedulerCache) cleanupAssumedPods(now time.Time) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	// The size of assumedPods should be small
	for key := range cache.assumedPods {
		ps, ok := cache.podStates[key]
		if !ok {
			panic("Key found in assumed set but not in podStates. Potentially a logical error.")
		}
		if !ps.bindingFinished {
			glog.Warningf("Couldn't expire cache for pod %v/%v. Binding is still in progress.",
				ps.pod.Namespace, ps.pod.Name)
			continue
		}
		if now.After(*ps.deadline) {
			glog.Warningf("Pod %s/%s expired", ps.pod.Namespace, ps.pod.Name)
			if err := cache.expirePod(key, ps); err != nil {
				glog.Errorf("ExpirePod failed for %s: %v", key, err)
			}
		}
	}
}

func (cache *schedulerCache) expirePod(key string, ps *podState) error {
	if err := cache.removePod(ps.pod); err != nil {
		return err
	}
	delete(cache.assumedPods, key)
	delete(cache.podStates, key)
	return nil
}
