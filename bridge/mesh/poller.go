package mesh

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"sync"
	"time"

	"github.com/golang/glog"
	crv1 "istio.io/pilot/bridge/api/pkg/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

type Poller struct {
	zoneSpec      crv1.ZoneSpec
	stop          chan struct{}
	importedSsMap *EndpointSubsetMap
	err           *PollerError
	mu            sync.RWMutex
}

type ExternalZonePollers map[string]*Poller

func NewPoller(zone crv1.ZoneSpec) *Poller {
	p := Poller{zone, make(chan struct{}, 1), nil, nil, sync.RWMutex{}}
	return &p
}

type PollerError struct {
	errMsg string
}

func NewPollerError(zoneName string, err error) *PollerError {
	return &PollerError{"Error fetching endpoints from zone " + zoneName + " Error: " + err.Error()}
}

func (e *PollerError) Error() string {
	return e.errMsg
}

func (p *Poller) updateError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pe := NewPollerError(p.zoneSpec.ZoneName, err)
	glog.Error(pe.Error())
	p.err = pe
}

func (p *Poller) GetZoneStatus() (ZoneDisplayInfo, *EndpointSubsetMap, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	zi := NewZoneDisplayInfo(p.zoneSpec.ZoneName, p.importedSsMap)
	if p.err != nil {
		return *zi, p.importedSsMap, &PollerError{p.err.errMsg}
	}
	return *zi, p.importedSsMap, nil
}

func (p *Poller) Run() {
	resp, err := http.Get("http://" + p.zoneSpec.MeshSyncAgentVip + "/mesh/v1/endpoints/")
	if err != nil {
		p.updateError(err)
		return
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		p.updateError(err)
		return
	}
	epsm := NewEndpointSubsetMap()
	err = json.Unmarshal(body, &epsm.epSubset)
	if err != nil {
		p.updateError(err)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.importedSsMap = epsm
	p.err = nil
}

func (pollers *ExternalZonePollers) Reconcile(ms crv1.MeshSpec, localZone string, currentRunInfo *MeshInfo) map[string]*EndpointSubsetMap {
	zonesToKeep := ExternalZonePollers{}
	zonesToAdd := ExternalZonePollers{}
	var done struct{}
	for _, zone := range ms.Zones {
		zoneName := zone.ZoneName
		if zoneName == localZone {
			continue
		}
		poller, found := (*pollers)[zoneName]
		switch {
		case !found:
			zonesToAdd[zone.ZoneName] = NewPoller(zone)
			break
		case zone.MeshSyncAgentVip != poller.zoneSpec.MeshSyncAgentVip:
			poller.stop <- done
			zonesToAdd[zoneName] = NewPoller(zone)
			delete(*pollers, zoneName)
			break
		case zone.MeshSyncAgentVip == poller.zoneSpec.MeshSyncAgentVip:
			zonesToKeep[zoneName] = poller
			delete(*pollers, zoneName)
			break
		}
	}
	// Remove unncessary ones
	for zoneName, poller := range *pollers {
		poller.stop <- done
		delete(*pollers, zoneName)
		glog.Infof("Removing zone '%s' from this mesh sync agent", zoneName)
	}

	// Add back pollers to keep
	// Get current info from stable zones
	zoneEpSsMap := make(map[string]*EndpointSubsetMap, len(zonesToKeep))
	for zoneName, poller := range zonesToKeep {
		(*pollers)[zoneName] = poller
		zdi, importedSsMap, err := poller.GetZoneStatus()
		currentRunInfo.AddZoneDisplayInfo(zdi)
		if err != nil {
			currentRunInfo.AddAgentWarning(err.Error())
		}
		zoneEpSsMap[zoneName] = importedSsMap
	}

	// Add new pollers
	for zoneName, poller := range zonesToAdd {
		(*pollers)[zoneName] = poller
		go wait.Until(poller.Run, time.Second, poller.stop)
		glog.Infof("Adding zone '%s' to this mesh sync agent", zoneName)
	}

	return zoneEpSsMap
}
