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

func (a *MeshSyncAgent) runWorker() {
	// Get expected state
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
		return
	} else {
		a.LocalZone = tmpZone
		a.healthSetter.SetHealth("Active")
	}
	ssMap := a.ssGetter.GetExpectedEndpointSubsets(a.LocalZone)
	opts := v1.ListOptions{}
	opts.LabelSelector = strings.Join([]string{MeshEndpointAnnotation, "true"}, "=")
	glog.Infof("Expected Endpoint Subsets: %v", ssMap)
	actualList, err := a.clientset.CoreV1().Endpoints("").List(opts)
	if err == nil {
		glog.Infof("Actual Endpoints: %v", actualList)
	} else {
		glog.Infof("Error fetching actual endpoints: %v", err)
	}
}

func (a *MeshSyncAgent) Run(stopCh chan struct{}) {
	glog.Info("Daemon initializing")
	go wait.Until(a.runWorker, time.Second, stopCh)
}

func (a *MeshSyncAgent) UpdateMesh(meshCrd *crv1.Mesh) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.meshCrd = meshCrd
}
