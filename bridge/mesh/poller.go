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
	err           *error
	mu            sync.RWMutex
}

type ExternalZonePollers map[string]*Poller

func NewPoller(zone crv1.ZoneSpec) *Poller {
	p := Poller{zone, make(chan struct{}, 1), nil, nil, sync.RWMutex{}}
	return &p
}

func (p *Poller) updateError(err *error) {
	glog.Errorf("Error fetching endpoints from zone %s: %v", p.zoneSpec.ZoneName, err)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *Poller) Run() {
	resp, err := http.Get("http://" + p.zoneSpec.MeshSyncAgentVip + "/mesh/v1/endpoints/")
	if err != nil {
		p.updateError(&err)
		return
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		p.updateError(&err)
		return
	}
	epSS := NewEndpointSubsetMap()
	err = json.Unmarshal(body, &epSS)
	if err != nil {
		p.updateError(&err)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.importedSsMap = epSS
	p.err = nil
}

func (pollers *ExternalZonePollers) Reconcile(ms crv1.MeshSpec, localZone string) {
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
	for zoneName, poller := range zonesToKeep {
		(*pollers)[zoneName] = poller
	}

	// Add new pollers
	for zoneName, poller := range zonesToAdd {
		(*pollers)[zoneName] = poller
		go wait.Until(poller.Run, time.Second, poller.stop)
		glog.Infof("Adding zone '%s' to this mesh sync agent", zoneName)
	}
}
