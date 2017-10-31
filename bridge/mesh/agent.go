package mesh

import (
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	crv1 "istio.io/pilot/bridge/api/pkg/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	MeshEndpointAnnotation = "config.istio.io/mesh.endpoint"
)

type MeshSyncAgent struct {
	ssGetter     ServiceEndpointSubsetGetter
	healthSetter HealthSetter
	meshCrd      *crv1.Mesh
	agentVips    map[string]bool
	LocalZone    string
	clientset    *kubernetes.Clientset
	mu           sync.RWMutex
}

type HealthSetter interface {
	SetHealth(newStatus string)
}

type ServiceEndpointSubsetGetter interface {
	GetExpectedEndpointSubsets(localZoneName string) EndpointSubsetMap
	GetAgentVips() map[string]bool
}

func NewMeshSyncAgent(clientset *kubernetes.Clientset, ssGetter ServiceEndpointSubsetGetter, healthSetter HealthSetter) *MeshSyncAgent {
	return &MeshSyncAgent{ssGetter, healthSetter, nil, map[string]bool{}, "", clientset, sync.RWMutex{}}
}

func (a *MeshSyncAgent) GetMeshSpec() crv1.MeshSpec {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.meshCrd != nil {
		return (*a.meshCrd).Spec
	}
	return crv1.MeshSpec{}
}

func (a *MeshSyncAgent) GetStatus() bool {
	a.agentVips = a.ssGetter.GetAgentVips()
	ms := a.GetMeshSpec()
	tmpZone := ""
	for _, z := range ms.Zones {
		vipParts := strings.Split(z.MeshSyncAgentVip, ":")
		if len(vipParts) > 0 {
			prefix := vipParts[0]
			_, vipFound := a.agentVips[prefix]
			if vipFound {
				tmpZone = z.ZoneName
				break
			}
		} else {
			continue
		}
	}
	if len(tmpZone) == 0 {
		glog.Warningf("Local MSA service VIP not found in Mesh Config.\nLocal VIP list: %v\nMesh Config:%v", a.agentVips, ms.Zones)
		a.LocalZone = ""
		a.healthSetter.SetHealth("Inactive")
		return false
	}
	a.LocalZone = tmpZone
	a.healthSetter.SetHealth("Active")
	return true
}

func (a *MeshSyncAgent) GetActualEndpointSubsets() EndpointSubsetMap {
	opts := v1.ListOptions{}
	opts.LabelSelector = strings.Join([]string{MeshEndpointAnnotation, "true"}, "=")
	m := NewEndpointSubsetMap()
	l, err := a.clientset.CoreV1().Endpoints("").List(opts)
	if err != nil {
		glog.Infof("Error fetching actual endpoints: %v", err)
	} else {
		m = NewEndpointSubsetMapFromList(l)
	}
	return *m
}

func (a *MeshSyncAgent) Reconcile(actual EndpointSubsetMap, expected EndpointSubsetMap) {
	var smMap, lgMap *EndpointSubsetMap
	inverted := false
	if len(actual.epSubset) > len(expected.epSubset) {
		smMap = &expected
		lgMap = &actual
	} else {
		smMap = &actual
		lgMap = &expected
		inverted = true
	}
	notInLgMap := NewEndpointSubsetMap()
	notSame := NewEndpointSubsetMap()
	for key, epsSm := range smMap.epSubset {
		epsLg, lgFound := lgMap.epSubset[key]
		if !lgFound {
			notInLgMap.epSubset[key] = epsSm
		} else {
			epsNotSame := false
			if len(epsSm.KeyEndpointMap) != len(epsLg.KeyEndpointMap) {
				epsNotSame = true
			} else {
				for _, epSm := range epsSm.KeyEndpointMap {
					_, epLgFound := epsLg.KeyEndpointMap[epSm.Key]
					if !epLgFound {
						epsNotSame = true
						break
					}
				}
			}
			if epsNotSame {
				// Maintain Name i.e. hashes from actual rather than ones from expected
				if !inverted {
					epsSm.Name = epsLg.Name
					notSame.epSubset[key] = epsSm
				} else {
					epsLg.Name = epsSm.Name
					notSame.epSubset[key] = epsLg
				}
			}
			delete(lgMap.epSubset, key)
		}
	}
	// Remainder of lgMap is not in smMap
	var createMap, delMap *EndpointSubsetMap
	updMap := notSame
	if !inverted {
		createMap = notInLgMap
		delMap = lgMap
	} else {
		createMap = lgMap
		delMap = notInLgMap
	}
	// CRUD actual endpoints
	// Start with delete map
	for _, eps := range delMap.epSubset {
		err := a.clientset.CoreV1().Endpoints(eps.Namespace).Delete(eps.Name, &v1.DeleteOptions{})
		if err != nil {
			glog.Warningf("Unable to delete mesh endpoint '%s'. Will try again.\n%v", eps.Name, err)
		}
	}
	// Then update
	for _, eps := range updMap.epSubset {
		vep := eps.ToK8sEndpoints()
		_, err := a.clientset.CoreV1().Endpoints(eps.Namespace).Update(&vep)
		if err != nil {
			glog.Warningf("Unable to update mesh endpoint '%v'. Will try again.\n%v", eps, err)
		}
	}
	// Finall create
	for _, eps := range createMap.epSubset {
		vep := eps.ToK8sEndpoints()
		_, err := a.clientset.CoreV1().Endpoints(eps.Namespace).Create(&vep)
		if err != nil {
			glog.Warningf("Unable to create mesh endpoint '%v'. Will try again.\n%v", eps, err)
		}
	}
}

func (a *MeshSyncAgent) UpdateMesh(meshCrd *crv1.Mesh) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.meshCrd = meshCrd
}

func (a *MeshSyncAgent) Run(stopCh chan struct{}) {
	glog.Info("Daemon initializing")
	go wait.Until(a.runWorker, time.Second, stopCh)
}

func (a *MeshSyncAgent) runWorker() {
	// Get status of MSA
	ok := a.GetStatus()
	if !ok {
		return
	}

	actualMap := a.GetActualEndpointSubsets()
	expectedMap := a.ssGetter.GetExpectedEndpointSubsets(a.LocalZone)
	a.Reconcile(actualMap, expectedMap)
}
