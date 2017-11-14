package mesh

import (
	"fmt"
	"net"
	"net/http"
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
	doNoOp         bool
	mu             sync.RWMutex
}

type ServiceEndpointSubsetGetter interface {
	GetExpectedEndpointSubsets(localZoneName string) EndpointSubsetMap
	GetAgentVips() map[string]bool
	GetServiceMap() map[string]*Service
}

type EndpointDisplayInfo struct {
	Namespace *string
	Service   *string
	Zone      *string
	CsvLabels *string
	AllLabels *string
	DnsName   *string
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

func NewMeshSyncAgent(clientset *kubernetes.Clientset, ssGetter ServiceEndpointSubsetGetter, globalInfo *MeshInfo, doNoOp bool) *MeshSyncAgent {
	return &MeshSyncAgent{ssGetter, globalInfo, MeshInfo{}, nil, map[string]bool{}, "", clientset, []byte("{}"),
		map[string]*EndpointSubsetMap{}, ExternalZonePollers{}, doNoOp, sync.RWMutex{}}
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
			msg := fmt.Sprintf("Zone '%s' is misconfigured. Illegal host port spec '%s' error: %s", z.ZoneName, z.MeshSyncAgentVip, err.Error())
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
		if zoneEpMap == nil {
			continue
		}
		for _, extEpss := range zoneEpMap.epSubset {
			if extEpss.Name == extEpss.Service {
				// This is a external service endpoint that was created in another zone, we'll create one for the local zone later.
				continue
			}
			zoneLocalExtEpss := extEpss.DeepCopy()
			zoneLocalExtEpss.AddExternalEndpointLabel()
			UpdateServiceDnsEndpointMap(&expectedExtServices, actualMap, zoneLocalExtEpss)
			expectedMap.epSubset[extEpss.Key] = zoneLocalExtEpss
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
			svcPort := new(corev1.ServicePort)
			svcPort.Name = dnsEpss.SrvPort
			svcPort.Protocol = corev1.ProtocolTCP
			svcPort.Port = dnsEpss.GetPort()
			newSvc := Service{svcKey, dnsEpss.Service, dnsEpss.Namespace, MeshExternalZoneSvc, map[string]string{}, "", svcPort}
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
	dnsEpss.Labels[LabelMeshEndpoint] = "true"
	dnsEpss.Labels[LabelMeshExternal] = "true"
	dnsEpss.Labels[LabelMeshDnsSrv] = DnsHostPort
	dnsEpss.Labels[LabelMeshDnsIp] = firstValue.PodIP
	return dnsEpss
}

func (a *MeshSyncAgent) GetActualEndpointSubsets(endpointLabel string, epssMap *EndpointSubsetMap) {
	opts := v1.ListOptions{}
	opts.LabelSelector = strings.Join([]string{endpointLabel, "true"}, "=")
	l, err := a.clientset.CoreV1().Endpoints("").List(opts)
	if err != nil {
		msg := fmt.Sprintf("Error fetching actual endpoints: %s", err.Error())
		a.currentRunInfo.AddAgentWarning(msg)
		glog.Error(msg)
	} else {
		BuildEndpointSubsetMapFromList(l, epssMap)
	}
}

func (a *MeshSyncAgent) ExecuteEndpointQuery(query *EndpointDisplayInfo) ([]*EndpointDisplayInfo, *string) {
	res := []*EndpointDisplayInfo{}
	hasLabels := query.CsvLabels != nil
	hasZone := query.Zone != nil
	hasService := query.Service != nil
	hasNamespace := query.Namespace != nil
	labels := map[string]string{}
	labels[LabelMeshEndpoint] = "true"
	if hasLabels {
		nvPairs := strings.Split(*query.CsvLabels, ",")
		for _, nvPair := range nvPairs {
			nameValues := strings.Split(nvPair, "=")
			if len(nameValues) != 2 {
				msg := fmt.Sprintf("Comma separated labels should be in the form name1=value1,name2=value2,...Found :'%s'", *query.CsvLabels)
				a.currentRunInfo.AddAgentWarning(msg)
				glog.Error(msg)
				return res, nil
			}
			labels[nameValues[0]] = nameValues[1]
		}
	}
	if hasZone {
		labels[LabelZone] = *query.Zone
	}
	Namespace := ""
	if hasNamespace {
		Namespace = *query.Namespace
	}
	Service := ""
	if hasService {
		Service = *query.Service
	}

	meshResolver := NewMeshResolver(a.clientset)
	var nsSvcEndpoints NamespaceServiceEndpoints
	var ResolvedIP *string
	var err error
	if query.DnsName == nil {
		nsSvcEndpoints, err = meshResolver.LookupSvcEndpointsBySvcName(Namespace, Service, labels)
	} else {
		ipAddrs, err := net.LookupHost(*query.DnsName)
		if err != nil {
			msg := err.Error()
			glog.Error(msg)
			return res, &msg
		}
		if len(ipAddrs) == 0 {
			msg := fmt.Sprintf("No IP addresses were found for DNS name '%s'", *query.DnsName)
			glog.Error(msg)
			return res, &msg
		}
		ResolvedIP = &ipAddrs[0]
		nsSvcEndpoints, query.Namespace, query.Service, err = meshResolver.LookupSvcEndpointsByIp(ipAddrs[0], labels)
		tmpZoneHack := ""
		query.Zone = &tmpZoneHack
	}
	if err != nil {
		msg := fmt.Sprintf("Error fetching endpoints: %s", err.Error())
		a.currentRunInfo.AddAgentWarning(msg)
		glog.Error(msg)
		return res, nil
	}
	sortedNs := nsSvcEndpoints.GetSortedNamespaces()
	for _, ns := range sortedNs {
		svcZoneEps, _ := nsSvcEndpoints[ns]
		sortedSvc := svcZoneEps.GetSortedServices()
		for _, svc := range sortedSvc {
			zoneEps, _ := svcZoneEps[svc]
			sortedZones := zoneEps.GetSortedZones()
			for _, zone := range sortedZones {
				isFirstZoneRow := true
				epssList, _ := zoneEps[zone]
				var zoneDispEpInfo *EndpointDisplayInfo
				for _, epss := range epssList {
					for _, ep := range epss.KeyEndpointMap {
						if isFirstZoneRow || (hasZone && hasService) {
							zoneDispEpInfo = &EndpointDisplayInfo{&epss.Namespace, &epss.Service, &epss.Zone, nil, nil, nil, []string{}}
							res = append(res, zoneDispEpInfo)
						}
						hostport := net.JoinHostPort(ep.PodIP, fmt.Sprintf("%d", ep.Port.ContainerPort))
						zoneDispEpInfo.HostPort = append(zoneDispEpInfo.HostPort, hostport)
						if hasZone && hasService {
							labels := []string{}
							for k, v := range ep.PodLabels {
								labels = append(labels, strings.Join([]string{k, v}, "="))
							}
							podLabels := strings.Join(labels, ",")
							zoneDispEpInfo.CsvLabels = &podLabels
						}
						isFirstZoneRow = false
					}
				}
			}
		}
	}
	return res, ResolvedIP
}

func (a *MeshSyncAgent) ReconcileExtServiceList(dnsSvcCreateSet map[string]*Service, dnsSvcDeleteSet map[string]*Service) {
	for _, svc := range dnsSvcDeleteSet {
		err := a.clientset.CoreV1().Services(svc.Namespace).Delete(svc.Name, &v1.DeleteOptions{})
		if err != nil {
			msg := fmt.Sprintf("Unable to delete mesh service. This may result in incorrect service entries for DNS. Will try again. Subset:\n%s\nError: %s\n", svc.Name, err.Error())
			a.currentRunInfo.AddAgentWarning(msg)
			glog.Error(msg)
		}
	}
	for _, svc := range dnsSvcCreateSet {
		corev1Svc := NewK8sServiceForDnsResolution(svc)
		_, err := a.clientset.CoreV1().Services(svc.Namespace).Create(corev1Svc)
		if err != nil {
			msg := fmt.Sprintf("Unable to create mesh service. This may result in incorrect service entries for DNS. Will try again. Subset:\n%s\nError: %s\n", svc.Name, err.Error())
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
			msg := fmt.Sprintf("Unable to delete mesh endpoint. Will try again. Subset:\n%s\nError: %s\n", epss.Name, err.Error())
			a.currentRunInfo.AddAgentWarning(msg)
			glog.Error(msg)
			a.currentRunInfo.IncrementCountEndpointErrors(1)
		} else {
			if glog.V(2) {
				glog.Infof("Deleted mesh endpoint. Subset\n%s\n", epss.Name)
			}
			a.currentRunInfo.IncrementCountEndpointsDeleted(1)
		}
	}
	// Then update
	for _, epss := range updateSet {
		vep := epss.ToK8sEndpoints()
		existingvep, err := a.clientset.CoreV1().Endpoints(vep.Namespace).Get(vep.Name, v1.GetOptions{})
		if err != nil {
			msg := fmt.Sprintf("Unable to update mesh endpoint. Cannot ascertain Resource Version. Will try again. Subset:\n%s\nError: %s\n", epss.Name, err.Error())
			a.currentRunInfo.AddAgentWarning(msg)
			glog.Error(msg)
			a.currentRunInfo.IncrementCountEndpointErrors(1)
		} else {
			vep.ResourceVersion = existingvep.ResourceVersion
			_, err := a.clientset.CoreV1().Endpoints(epss.Namespace).Update(&vep)
			if err != nil {
				msg := fmt.Sprintf("Unable to update mesh endpoint. Will try again. Subset:\n%s\nError: %s\n", epss.Name, err.Error())
				a.currentRunInfo.AddAgentWarning(msg)

				glog.Error(msg)
				a.currentRunInfo.IncrementCountEndpointErrors(1)
			} else {
				if glog.V(2) {
					glog.Infof("Updated mesh endpoint. Subset\n%s\n", epss.Name)
				}
				a.currentRunInfo.IncrementCountEndpointsUpdated(1)
			}
		}
	}
	// Finally create
	for _, epss := range createSet {
		vep := epss.ToK8sEndpoints()
		_, err := a.clientset.CoreV1().Endpoints(epss.Namespace).Create(&vep)
		if err != nil {
			msg := fmt.Sprintf("Unable to create mesh endpoint. Will try again. Subset:\n%s\nError: %s\n", epss.Name, err.Error())

			// *******************************************************
			// TODO - Fix bug causing this warning
			// a.currentRunInfo.AddAgentWarning(msg)

			glog.Error(msg)
			// a.currentRunInfo.IncrementCountEndpointErrors(1)
			// *******************************************************
		} else {
			if glog.V(2) {
				glog.Infof("Created mesh endpoint. Subset\n%s\n", epss.Name)
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

	if a.doNoOp {
		a.currentRunInfo.SetLabel(ServerStatus, "NoOp")
		return
	}

	// Get status of MSA
	ok := a.GetMeshStatus()
	if !ok {
		a.currentRunInfo.SetLabel(ServerStatus, "Inactive")
		return
	}
	a.currentRunInfo.SetLabel(ServerStatus, "Active")
	actualMap := NewEndpointSubsetMap()
	a.GetActualEndpointSubsets(LabelMeshEndpoint, actualMap)
	a.currentRunInfo.AddZoneDisplayInfo(*NewZoneDisplayInfo(a.localZone, actualMap))
	expectedMap := a.ssGetter.GetExpectedEndpointSubsets(a.localZone)
	a.ExportLocalEndpointSubsets(expectedMap.ToJson()) // For what it's worth, this is what is available right now
	if glog.V(2) {
		glog.Infof("\n\nPre reconciliation: Actual ss count: '%d', Expected ss count: '%d'", len(actualMap.epSubset), len(expectedMap.epSubset))
	}
	expectedExtServices := a.AddExternalEndpoints(actualMap, &expectedMap)
	a.Reconcile(*actualMap, expectedMap, expectedExtServices)
}
