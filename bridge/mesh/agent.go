package mesh

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	meshv1 "istio.io/pilot/bridge/api/pkg/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

type MeshSyncAgent struct {
	ssGetter       ServiceEndpointSubsetGetter
	globalInfo     *MeshInfo
	currentRunInfo MeshInfo
	meshCrd        *meshv1.Mesh
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
	GetServiceMap() map[string]*Service
}

type EndpointDisplayInfo struct {
	Service   *string
	Namespace *string
	Zone      *string
	CsvLabels *string
	AllLabels *string
	HostPort  []string
}

type DnsEpInfo struct {
	// Key = Namespace + "/" +ServiceName
	Key              string
	Namespace        string
	ServiceName      string
	ExistingHostPort string
	MatchingEp       *EndpointSubset
	CandidateEp      *EndpointSubset
}

func GetExpectedDnsEpInfoKey(expectedEpSubset *EndpointSubset) string {
	if expectedEpSubset.Namespace != "" {
		return expectedEpSubset.Namespace + "/" + expectedEpSubset.Service
	} else {
		return expectedEpSubset.Service
	}
}

func GetActualDnsEpInfoKey(vep *corev1.Endpoints) string {
	if vep.Namespace != "" {
		return vep.Namespace + "/" + vep.Name
	}
	return vep.Name
}

func NewDnsEpInfoFromEpSubset(expectedEpSubset *EndpointSubset) *DnsEpInfo {
	dnsEpInfo := DnsEpInfo{GetExpectedDnsEpInfoKey(expectedEpSubset), expectedEpSubset.Namespace, expectedEpSubset.Service, "", nil, expectedEpSubset}
	return &dnsEpInfo
}

func NewDnsEpInfoFromHostPortAndEpSubset(HostPort string, expectedEpSubset *EndpointSubset) *DnsEpInfo {
	dnsEpInfo := NewDnsEpInfoFromEpSubset(expectedEpSubset)
	dnsEpInfo.ExistingHostPort = HostPort
	return dnsEpInfo
}

func NewMeshSyncAgent(clientset *kubernetes.Clientset, ssGetter ServiceEndpointSubsetGetter, globalInfo *MeshInfo) *MeshSyncAgent {
	return &MeshSyncAgent{ssGetter, globalInfo, MeshInfo{}, nil, map[string]bool{}, "", clientset, []byte("{}"),
		map[string]*EndpointSubsetMap{}, ExternalZonePollers{}, sync.RWMutex{}}
}

func (a *MeshSyncAgent) GetMeshSpec() meshv1.MeshSpec {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.meshCrd != nil {
		return (*a.meshCrd).Spec
	}
	return meshv1.MeshSpec{}
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
	a.importedEp = a.zonePollers.Reconcile(ms, a.localZone, &a.currentRunInfo)
	return true
}

func (a *MeshSyncAgent) AddExternalEndpoints(actualMap *EndpointSubsetMap, expectedMap *EndpointSubsetMap) *map[string]*DnsEpInfo {
	expectedExtServices := map[string]*DnsEpInfo{}
	for _, zoneEpMap := range a.importedEp {
		for _, extEpss := range zoneEpMap.epSubset {
			if extEpss.Name == extEpss.Service {
				// This is a external service endpoint that was created in another zone, we'll create one for the local zone later.
				continue
			}
			UpdateServiceDnsEndpointMap(&expectedExtServices, actualMap, extEpss)
			expectedMap.epSubset[extEpss.Key] = extEpss.DeepCopy()
		}
	}
	return &expectedExtServices
}

func (a *MeshSyncAgent) AddExpectedDnsEndpoints(expectedExtServices *map[string]*DnsEpInfo, expectedMap *EndpointSubsetMap) (map[string]*Service, map[string]*Service) {
	serviceMap := a.ssGetter.GetServiceMap()
	extSvcCreateSet := map[string]*Service{}
	extSvcDeleteSet := map[string]*Service{}
	for svcKey, svc := range serviceMap {
		if svc.SvcType == MeshExternalZoneSvc {
			extSvcDeleteSet[svcKey] = svc
		}
	}
	for svcKey, dnsEpInfo := range *expectedExtServices {
		var dnsEpss *EndpointSubset
		if dnsEpInfo.ExistingHostPort == "" {
			dnsEpss = dnsEpInfo.CandidateEp
		} else {
			dnsEpss = dnsEpInfo.MatchingEp
		}
		_, svcFound := serviceMap[svcKey]
		if !svcFound {
			newSvc := Service{svcKey, dnsEpss.Service, dnsEpss.Namespace, MeshExternalZoneSvc, map[string]string{}, ""}
			extSvcCreateSet[svcKey] = &newSvc
		} else {
			delete(extSvcDeleteSet, svcKey)
		}
		dnsEp := CreateDnsEpSubsetFromExternalEpSubset(dnsEpss)
		expectedMap.epSubset[dnsEp.Key] = dnsEp
	}

	return extSvcCreateSet, extSvcDeleteSet
}

func UpdateServiceDnsEndpointMap(expectedExtServices *map[string]*DnsEpInfo, actualMap *EndpointSubsetMap, extEpss *EndpointSubset) {
	ExtServiceKey := GetExpectedDnsEpInfoKey(extEpss)
	extSvcDnsInfo, hasExtService := (*expectedExtServices)[ExtServiceKey]
	if !hasExtService {
		// This service needs to have its DNS endpoint reconciled
		// Check is this service currently has a DnsEndpoint in the actual map
		// TODO(gnirodi): check key contents work before submit
		dnsEpss := CreateDnsEpSubsetFromExternalEpSubset(extEpss)
		var extSvcDnsInfo *DnsEpInfo
		actualDnsEpss, hasActualDnsEpss := actualMap.epSubset[ExtServiceKey]
		if !hasActualDnsEpss {
			// Use extEpss as the best candidate for the Dns Endpoint
			extSvcDnsInfo = NewDnsEpInfoFromEpSubset(dnsEpss)
		} else {
			// This service has an actual DNS endpoint
			actualHostPort, hostPortFound := actualDnsEpss.Labels[LabelMeshDnsSrv]
			if !hostPortFound {
				// Use extEpss as the best candidate for the Dns Endpoint
				extSvcDnsInfo = NewDnsEpInfoFromEpSubset(dnsEpss)
			} else {
				extSvcDnsInfo = NewDnsEpInfoFromHostPortAndEpSubset(actualHostPort, actualDnsEpss)
			}
		}
		// Create the DnsEpInfo so that hasExtService returns true for subsequent
		// endpoints that have the same service
		(*expectedExtServices)[ExtServiceKey] = extSvcDnsInfo
	}
	if extSvcDnsInfo.ExistingHostPort != "" {
		_, hostPortFoundInExt := extEpss.HostPortSet[extSvcDnsInfo.ExistingHostPort]
		if hostPortFoundInExt {
			extSvcDnsInfo.MatchingEp = extEpss
		}
	}
}

func CreateDnsEpSubsetFromExternalEpSubset(extEpss *EndpointSubset) *EndpointSubset {
	dnsEpss := extEpss.DeepCopy()
	dnsEpss.Name = extEpss.Service
	dnsEpss.Key = GetExpectedDnsEpInfoKey(extEpss)
	// We retain only one host port. We should consider removing this restriction.
	var firstValue *Endpoint
	firstValue = nil
	for k, v := range dnsEpss.KeyEndpointMap {
		if firstValue == nil {
			firstValue = v
			continue
		}
		delete(dnsEpss.KeyEndpointMap, k)
	}
	DnsHostPort := net.JoinHostPort(firstValue.PodIP, fmt.Sprintf("%d", firstValue.Port.ContainerPort))
	dnsEpss.HostPortSet = map[string]bool{DnsHostPort: true}
	dnsEpss.HostPortSet[DnsHostPort] = true
	dnsEpss.Labels[LabelMeshDnsSrv] = DnsHostPort
	dnsEpss.Labels[LabelMeshDnsIp] = firstValue.PodIP
	return dnsEpss
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

func (a *MeshSyncAgent) ExecuteEndpointQuery(query *EndpointDisplayInfo) []EndpointDisplayInfo {
	res := []EndpointDisplayInfo{}
	hasZone := query.Zone != nil
	hasService := query.Service != nil
	hasNamespace := query.Namespace != nil
	hasLabels := query.CsvLabels != nil
	labels := []string{LabelMeshEndpoint + "=true"}
	if hasLabels {
		labels = strings.Split(*query.CsvLabels, ",")
	}
	if hasZone {
		labels = append(labels, LabelZone+"="+*query.Zone)
	}
	if hasService {
		labels = append(labels, LabelService+"="+*query.Service)
	}
	Namespace := ""
	if hasNamespace {
		Namespace = *query.Namespace
	}
	opts := v1.ListOptions{}
	opts.LabelSelector = strings.Join(labels, ",")
	l, err := a.clientset.CoreV1().Endpoints(Namespace).List(opts)
	if err != nil {
		msg := fmt.Sprintf("Error fetching endpoints: %s", err.Error())
		a.currentRunInfo.AddAgentWarning(msg)
		glog.Error(msg)
	} else {
		m := NewEndpointSubsetMapFromList(l)
		groupByKeyMap := map[string]EndpointDisplayInfo{}
		for _, epss := range m.epSubset {
			switch {
			case hasZone && !hasService:
				// Group by service
				AddEndpointSubset(groupByKeyMap, epss.Service, epss)
				break
			case hasService && !hasZone:
				// Group by zone
				AddEndpointSubset(groupByKeyMap, epss.Zone, epss)
				break
			case hasService && hasZone:
				// No grouping
				AddEndpointSubset(groupByKeyMap, "", epss)
				break
			}
		}
		orderedKeys := make([]string, len(groupByKeyMap))
		ki := 0
		for k, _ := range groupByKeyMap {
			orderedKeys[ki] = k
			ki++
		}
		sort.Strings(orderedKeys)
		for _, k := range orderedKeys {
			v, _ := groupByKeyMap[k]
			res = append(res, v)
		}
	}
	return res
}

func AddEndpointSubset(groupByKeyMap map[string]EndpointDisplayInfo, key string, epss *EndpointSubset) {
	if key == "" {
		for _, ep := range epss.KeyEndpointMap {
			epdi := EndpointDisplayInfo{&epss.Service, &epss.Namespace, &epss.Zone, nil, nil,
				[]string{ep.PodIP + ":" + fmt.Sprintf("%d", ep.Port.ContainerPort)}}
			labels := []string{}
			for k, v := range ep.PodLabels {
				labels = append(labels, strings.Join([]string{k, v}, "="))
			}
			podLabels := strings.Join(labels, ",")
			epdi.CsvLabels = &podLabels
			groupByKeyMap[fmt.Sprintf("%d", len(groupByKeyMap))] = epdi
		}
		return
	}
	// Needs grouping
	epdi, found := groupByKeyMap[key]
	if !found {
		epdi = EndpointDisplayInfo{&epss.Service, &epss.Namespace, &epss.Zone, nil, nil, []string{}}
	}
	for _, ep := range epss.KeyEndpointMap {
		hostport := net.JoinHostPort(ep.PodIP, fmt.Sprintf("%d", ep.Port.ContainerPort))
		epdi.HostPort = append(epdi.HostPort, hostport)
	}
	groupByKeyMap[key] = epdi
}

func (a *MeshSyncAgent) ReconcileExtServiceList(dnsSvcCreateSet map[string]*Service, dnsSvcDeleteSet map[string]*Service) {
	for _, svc := range dnsSvcDeleteSet {
		err := a.clientset.CoreV1().Services(svc.Namespace).Delete(svc.Name, &v1.DeleteOptions{})
		if err != nil {
			msg := fmt.Sprintf("Unable to delete mesh service. This may result in incorrect service entries for DNS. Will try again. Subset:\n%v\nError: %v\n", svc, err)
			a.currentRunInfo.AddAgentWarning(msg)
			glog.Error(msg)
		}
	}
	for _, svc := range dnsSvcCreateSet {
		corev1Svc := NewK8sServiceForDnsResolution(svc)
		_, err := a.clientset.CoreV1().Services(svc.Namespace).Create(corev1Svc)
		if err != nil {
			msg := fmt.Sprintf("Unable to create mesh service. This may result in incorrect service entries for DNS. Will try again. Subset:\n%v\nError: %v\n", svc, err)
			a.currentRunInfo.AddAgentWarning(msg)
			glog.Error(msg)
		}
	}
}

func (a *MeshSyncAgent) Reconcile(actual EndpointSubsetMap, expected EndpointSubsetMap, expectedExtServices *map[string]*DnsEpInfo) {
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
		// Prep the delete set with the full set of keys
		// For ones that need to be updated, we delete the key
		// so that remaining ones are the ones that need deletion
		deleteSet[k] = v
	}

	dnsSvcCreateSet, dnsSvcDeleteSet := a.AddExpectedDnsEndpoints(expectedExtServices, &expected)

	for key, epssExpected := range expected.epSubset {
		if glog.V(2) {
			if expectedLogInfo == "" {
				expectedLogInfo = "\nExpected Key Set:\n"
			}
			expectedLogInfo = expectedLogInfo + epssExpected.Name + " " + key + "\n"
		}
		epssActual, actualFound := actual.epSubset[key]
		if !actualFound {
			createSet[key] = epssExpected
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
				updateSet[key] = epssExpected
			}
			delete(deleteSet, key)
		}
	}

	glog.V(2).Info(expectedLogInfo, actualLogInfo, "\n")

	// CRUD services that need to be created for enabling DNS lookups
	a.ReconcileExtServiceList(dnsSvcCreateSet, dnsSvcDeleteSet)

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

func (a *MeshSyncAgent) UpdateMesh(meshCrd *meshv1.Mesh) {
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
	expectedExtServices := a.AddExternalEndpoints(&actualMap, &expectedMap)
	a.Reconcile(actualMap, expectedMap, expectedExtServices)
}
