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

// Map from namespace to NamespacePods
type NamespacePodsMap map[string]NamespacePods

// Map from pod key to namespace of the pod
type PodKeyNamespaceMap map[string]string

// Container for Pods belonging in the local mesh
type LocalPodList struct {
	nsIgnoreRegex      *regexp.Regexp
	namespacePodsMap   NamespacePodsMap
	podKeyNamespaceMap PodKeyNamespaceMap
	mu                 sync.RWMutex
}

func NewLocalPodList(nsIgnoreRegex string) *LocalPodList {
	regex, err := regexp.Compile(nsIgnoreRegex)
	if err != nil {
		glog.Fatal("Error compiling Namespace exclude regex")
	}
	return &LocalPodList{regex, NamespacePodsMap{}, PodKeyNamespaceMap{}, sync.RWMutex{}}
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

func (l *LocalPodList) UpdatePod(key string, pod *v1.Pod) {
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
			nsPods = NamespacePods{}
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
		mshpd := Pod{key, ns, map[string]string{}, make([]Endpoint, len(podPorts))}

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
		epTemplate := NewEndpoint(ns, "", pod.Status.PodIP, v1.ContainerPort{}, mshpd.Labels)
		for _, port := range podPorts {
			mshpdEpTemplate := epTemplate.DeepCopy()
			mshpdEpTemplate.Port = port
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

func (l *LocalPodList) UpdateService(keySvcMap *map[string]Service) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, svc := range *keySvcMap {
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
			// All other keysets much match
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
	}
}
