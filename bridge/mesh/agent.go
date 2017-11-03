package mesh

import (
	"fmt"
	"net"
	"net/http"
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
	LabelMeshEndpoint = "config.istio.io/mesh.endpoint"
)

type MeshSyncAgent struct {
	ssGetter       ServiceEndpointSubsetGetter
	globalInfo     *MeshInfo
	currentRunInfo MeshInfo
	meshCrd        *crv1.Mesh
	agentVips      map[string]bool
	localZone      string
	clientset      *kubernetes.Clientset
	exportedEp     []byte
	importedEp     map[string]*EndpointSubsetMap
	zonePollers    ExternalZonePollers
	mu             sync.RWMutex
}

type ServiceEndpointSubsetGetter interface {
	GetExpectedEndpointSubsets(localZoneName string) EndpointSubsetMap
	GetAgentVips() map[string]bool
}

func NewMeshSyncAgent(clientset *kubernetes.Clientset, ssGetter ServiceEndpointSubsetGetter, globalInfo *MeshInfo) *MeshSyncAgent {
	return &MeshSyncAgent{ssGetter, globalInfo, MeshInfo{}, nil, map[string]bool{}, "", clientset, []byte("{}"),
		map[string]*EndpointSubsetMap{}, ExternalZonePollers{}, sync.RWMutex{}}
}

func (a *MeshSyncAgent) GetMeshSpec() crv1.MeshSpec {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.meshCrd != nil {
		return (*a.meshCrd).Spec
	}
	return crv1.MeshSpec{}
}

func (a *MeshSyncAgent) GetExportedEndpoints() []byte {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.exportedEp
}

func (a *MeshSyncAgent) ExportLocalEndpointSubsets(jsonMap []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.exportedEp = jsonMap
}

func (a *MeshSyncAgent) GetMeshStatus() bool {
	a.agentVips = a.ssGetter.GetAgentVips()
	ms := a.GetMeshSpec()
	tmpZone := ""
	if len(ms.Zones) == 0 {
		msg := "Local MSA service missing Mesh configuration."
		a.currentRunInfo.AddAgentWarning(msg)
		glog.Warning(msg)
		a.localZone = ""
		return false
	}
	for _, z := range ms.Zones {
		host, _, err := net.SplitHostPort(z.MeshSyncAgentVip)
		if err != nil {
			msg := fmt.Sprintf("Zone '%s' is misconfigured. Illegal host port spec '%s' error: %v", z.ZoneName, z.MeshSyncAgentVip, err)
			a.currentRunInfo.AddAgentWarning(msg)
			glog.Error(msg)
			continue
		}
		_, vipFound := a.agentVips[host]
		if vipFound {
			tmpZone = z.ZoneName
			continue
		}
	}
	if tmpZone == "" {
		msg := fmt.Sprintf("Local MSA service VIP not found in Mesh Config.\nLocal VIP list: %v\nMesh Config:%v", a.agentVips, ms.Zones)
		a.currentRunInfo.AddAgentWarning(msg)
		glog.Warning(msg)
		a.localZone = ""
		return false
	}
	a.localZone = tmpZone
	a.zonePollers.Reconcile(ms, a.localZone, &a.currentRunInfo)
	return true
}

func (a *MeshSyncAgent) GetActualEndpointSubsets() EndpointSubsetMap {
	opts := v1.ListOptions{}
	opts.LabelSelector = strings.Join([]string{LabelMeshEndpoint, "true"}, "=")
	m := NewEndpointSubsetMap()
	l, err := a.clientset.CoreV1().Endpoints("").List(opts)
	if err != nil {
		msg := fmt.Sprintf("Error fetching actual endpoints: %v", err)
		a.currentRunInfo.AddAgentWarning(msg)
		glog.Error(msg)
	} else {
		m = NewEndpointSubsetMapFromList(l)
	}
	return *m
}

func (a *MeshSyncAgent) Reconcile(actual EndpointSubsetMap, expected EndpointSubsetMap) {
	var expectedLogInfo, actualLogInfo string
	createSet := map[string]*EndpointSubset{}
	updateSet := map[string]*EndpointSubset{}
	deleteSet := map[string]*EndpointSubset{}
	// Make a copy and delete matching keys on iteration
	// Remaining are ones that need to be deleted
	for k, v := range actual.epSubset {
		if glog.V(2) {
			if actualLogInfo == "" {
				actualLogInfo = "\nActual Key Set:\n"
			}
			actualLogInfo = actualLogInfo + v.Name + " " + k + "\n"
		}
		deleteSet[k] = &v
	}
	for key, epssExpected := range expected.epSubset {
		if glog.V(2) {
			if expectedLogInfo == "" {
				expectedLogInfo = "\nExpected Key Set:\n"
			}
			expectedLogInfo = expectedLogInfo + epssExpected.Name + " " + key + "\n"
		}
		epssActual, actualFound := actual.epSubset[key]
		if !actualFound {
			createSet[key] = &epssExpected
		} else {
			epssNotSame := false
			if len(epssExpected.KeyEndpointMap) != len(epssActual.KeyEndpointMap) {
				epssNotSame = true
			} else {
				for _, epExpected := range epssExpected.KeyEndpointMap {
					_, epActualFound := epssActual.KeyEndpointMap[epExpected.Key]
					if !epActualFound {
						epssNotSame = true
						break
					}
				}
			}
			if epssNotSame {
				updateSet[key] = &epssExpected
			}
			delete(deleteSet, key)
		}
	}

	glog.V(2).Info(expectedLogInfo, actualLogInfo, "\n")

	// CRUD actual endpoints
	// Start with delete map
	for _, epss := range deleteSet {
		err := a.clientset.CoreV1().Endpoints(epss.Namespace).Delete(epss.Name, &v1.DeleteOptions{})
		if err != nil {
			msg := fmt.Sprintf("Unable to delete mesh endpoint. Will try again. Subset:\n%v\nError: %v\n", epss, err)
			a.currentRunInfo.AddAgentWarning(msg)
			glog.Error(msg)
			a.currentRunInfo.IncrementCountEndpointErrors(1)
		} else {
			if glog.V(2) {
				glog.Infof("Deleted mesh endpoint. Subset\n%v\n", epss)
			}
			a.currentRunInfo.IncrementCountEndpointsDeleted(1)
		}
	}
	// Then update
	for _, epss := range updateSet {
		vep := epss.ToK8sEndpoints()
		_, err := a.clientset.CoreV1().Endpoints(epss.Namespace).Update(&vep)
		if err != nil {
			msg := fmt.Sprintf("Unable to update mesh endpoint. Will try again. Subset:\n%v\nError: %v\n", epss, err)
			a.currentRunInfo.AddAgentWarning(msg)

			glog.Error(msg)
			a.currentRunInfo.IncrementCountEndpointErrors(1)
		} else {
			if glog.V(2) {
				glog.Infof("Updated mesh endpoint. Subset:\n%v\n", epss)
			}
			a.currentRunInfo.IncrementCountEndpointsUpdated(1)
		}
	}
	// Finall create
	for _, epss := range createSet {
		vep := epss.ToK8sEndpoints()
		_, err := a.clientset.CoreV1().Endpoints(epss.Namespace).Create(&vep)
		if err != nil {
			msg := fmt.Sprintf("Unable to create mesh endpoint. Will try again. Subset:\n%v\nError: %v\n", epss, err)
			a.currentRunInfo.AddAgentWarning(msg)

			glog.Error(msg)
			a.currentRunInfo.IncrementCountEndpointErrors(1)
		} else {
			if glog.V(2) {
				glog.Infof("Created mesh endpoint. Subset:\n%v\n", epss)
			}
			a.currentRunInfo.IncrementCountEndpointsCreated(1)
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

	// Start endpoint for cross zone sync
	http.HandleFunc("/mesh/v1/endpoints/", func(w http.ResponseWriter, r *http.Request) {
		b := a.GetExportedEndpoints()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(b)
	})

	go wait.Until(a.runWorker, time.Second, stopCh)
	<-stopCh
}

func (a *MeshSyncAgent) runWorker() {
	a.currentRunInfo = *NewMeshInfo()
	defer a.globalInfo.SetStatus(&a.currentRunInfo)
	// Get status of MSA
	ok := a.GetMeshStatus()
	if !ok {
		a.currentRunInfo.SetLabel(ServerStatus, "Inactive")
		return
	}
	a.currentRunInfo.SetLabel(ServerStatus, "Active")
	actualMap := a.GetActualEndpointSubsets()
	a.currentRunInfo.AddZoneDisplayInfo(ZoneDisplayInfo{a.localZone, len(actualMap.epSubset)})
	a.ExportLocalEndpointSubsets(actualMap.ToJson()) // For what it's worth, this is what is available right now
	expectedMap := a.ssGetter.GetExpectedEndpointSubsets(a.localZone)
	if glog.V(2) {
		glog.Infof("\n\nPre reconciliation: Actual ss count: '%d', Expected ss count: '%d'", len(actualMap.epSubset), len(expectedMap.epSubset))
	}
	a.Reconcile(actualMap, expectedMap)
}
