package mesh

import (
	"math"
	"regexp"
	"sync"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
)

// Pod representation within a Mesh
type Pod struct {
	// The key of the pod is expected to be unique within a given namespace of the local zone
	Key         string
	Namespace   string
	Labels      map[string]string
	EpTemplates []Endpoint
}

// Map of k8s Pod key to the mesh Pod
type KeyPodMap map[string]Pod

// Set of k8s local keys that matched a label name and value
type PodKeySet map[string]bool

// Map of label value to PodKeySet
type ValueMap map[string]PodKeySet

// Map of label to ValueMap
type LabelMap map[string]ValueMap

// Struct containing mesh Pods for a given namespace
type NamespacePods struct {
	Namespace string
	keyPodMap KeyPodMap
	labelMap  LabelMap
}

func NewNamespacePods(ns string) *NamespacePods {
	return &NamespacePods{ns, KeyPodMap{}, LabelMap{}}
}

// Map from namespace to NamespacePods
type NamespacePodsMap map[string]NamespacePods

// Map from pod key to namespace of the pod
type PodKeyNamespaceMap map[string]string

// Container for Pods belonging in the local zone
type PodList struct {
	nsIgnoreRegex      *regexp.Regexp
	namespacePodsMap   NamespacePodsMap
	podKeyNamespaceMap PodKeyNamespaceMap
	mu                 sync.RWMutex
}

func NewPodList(nsIgnoreRegex string) *PodList {
	regex, err := regexp.Compile(nsIgnoreRegex)
	if err != nil {
		glog.Fatal("Error compiling Namespace exclude regex")
	}
	return &PodList{regex, NamespacePodsMap{}, PodKeyNamespaceMap{}, sync.RWMutex{}}
}

func deletePodReferences(key string, nsPods NamespacePods, keyNs PodKeyNamespaceMap) {
	defer delete(nsPods.keyPodMap, key)
	defer delete(keyNs, key)
	pd, pdFound := nsPods.keyPodMap[key]
	if !pdFound {
		return
	}
	for k, v := range pd.Labels {
		vm, vmFound := nsPods.labelMap[k]
		if !vmFound {
			continue
		}
		ks, ksFound := vm[v]
		if !ksFound {
			continue
		}
		delete(ks, key)
	}
}

func (l *PodList) UpdatePod(key string, pod *v1.Pod) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if pod != nil {
		if l.nsIgnoreRegex.MatchString(pod.Namespace) {
			glog.V(2).Infof("Ignoring Pod named '%s' from namespace '%s' due to namespace exclude regex '%s'", pod.Name, pod.Namespace, l.nsIgnoreRegex.String())
			return
		}

		// Assign namespace
		ns := pod.Namespace
		if ns == "" {
			ns = v1.NamespaceDefault
		}
		nsPods, nsFound := l.namespacePodsMap[ns]
		if !nsFound {
			nsPods = *NewNamespacePods(ns)
			l.namespacePodsMap[ns] = nsPods
		}
		// Setup label references, but first delete references for an existing object
		deletePodReferences(key, nsPods, l.podKeyNamespaceMap)

		// Filter out pods that will never match
		if pod.Status.Phase != v1.PodRunning {
			return
		}
		if len(pod.Labels) == 0 {
			return
		}
		if pod.Status.PodIP == "" {
			return
		}
		podPorts := []v1.ContainerPort{}
		for _, c := range pod.Spec.Containers {
			for _, p := range c.Ports {
				podPorts = append(podPorts, p)
			}
		}
		if len(podPorts) == 0 {
			return
		}

		// Creat Mesh pod representation
		mshpd := Pod{key, ns, map[string]string{}, []Endpoint{}}

		// Index pod label and values
		for k, v := range pod.Labels {
			mshpd.Labels[k] = v
			vm, hasLbl := nsPods.labelMap[k]
			if !hasLbl {
				vm = ValueMap{}
				nsPods.labelMap[k] = vm
			}
			pks, hasVal := vm[v]
			if !hasVal {
				pks = PodKeySet{}
				vm[v] = pks
			}
			pks[key] = true
		}

		// Build Endpoint templates for the pod
		epTemplate := NewEndpoint(ns, "", "", pod.Status.PodIP, v1.ContainerPort{}, mshpd.Labels)
		for _, port := range podPorts {
			mshpdEpTemplate := epTemplate.DeepCopy()
			mshpdEpTemplate.SetPort(port)
			mshpdEpTemplate.ComputeKeyForSortedLabels()
			mshpd.EpTemplates = append(mshpd.EpTemplates, mshpdEpTemplate)
		}

		// Add the mesh pod to the list
		nsPods.keyPodMap[key] = mshpd
		l.podKeyNamespaceMap[key] = ns
	} else {
		defer delete(l.podKeyNamespaceMap, key)
		ns, nsFound := l.podKeyNamespaceMap[key]
		if !nsFound {
			return
		}
		nsPods, nsPodsFound := l.namespacePodsMap[ns]
		if !nsPodsFound {
			glog.Warningf("Namespace '%s' not found for deleted mesh pod with key '%s'. This ought not to happen", ns, key)
			return
		}
		// Delete references for an existing object
		deletePodReferences(key, nsPods, l.podKeyNamespaceMap)
	}
}

func (l *PodList) GetExpectedEndpointSubsets(localZoneName string, keySvcMap *map[string]Service) EndpointSubsetMap {
	l.mu.RLock()
	defer l.mu.RUnlock()
	esm := NewEndpointSubsetMap()
	for _, svc := range *keySvcMap {
		if svc.SvcType != MeshService {
			continue
		}

		// Get Pods for the service's namespace
		ns := svc.Namespace
		if ns == "" {
			ns = v1.NamespaceDefault
		}
		nsPods, nsFound := l.namespacePodsMap[ns]
		if !nsFound {
			continue
		}

		// Get pods that match all label values for the service
		minPksLen := math.MaxInt32
		minPksKey := ""
		// Label name to PodKeySet that has matching values
		lblPksMap := map[string]PodKeySet{}
		allKeyValsFound := true // All labels must have values
		for k, v := range svc.Labels {
			vm, vmFound := nsPods.labelMap[k]
			if !vmFound || len(vm) == 0 {
				allKeyValsFound = false
				break
			}
			pks, pksFound := vm[v]
			if !pksFound || len(pks) == 0 {
				allKeyValsFound = false
				break
			}
			lenPks := len(pks)
			if minPksLen > lenPks {
				minPksLen = lenPks
				minPksKey = k
			}
			lblPksMap[k] = pks
		}
		if !allKeyValsFound {
			continue
		}

		// The intersection of PodKeySets between all lables is the final pod set
		intPks := PodKeySet{}
		minPks := lblPksMap[minPksKey]
		delete(lblPksMap, minPksKey)
		for mshpdKey, _ := range minPks {
			// All other keysets much match each pod keyset for the other labels
			allKeysMatch := true
			for _, pks := range lblPksMap {
				_, hasKey := pks[mshpdKey]
				if !hasKey {
					allKeysMatch = false
					break
				}
			}
			if !allKeysMatch {
				continue
			}
			intPks[mshpdKey] = true
		}
		// We have the exact set of pods that match the service spec
		for pk, _ := range intPks {
			mshpd, _ := nsPods.keyPodMap[pk]
			for _, t := range mshpd.EpTemplates {
				esm.AddEndpoint(&t, svc.Name, localZoneName)
			}
		}
	}
	return *esm
}
