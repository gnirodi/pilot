package mesh

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	k8sServiceDomainNameFormat = "_<portname>._<protocol>.my-service.my-namespace.svc.cluster.local"
)

type MeshResolver struct {
	clientset *kubernetes.Clientset
}

type ResolverError struct {
	errMsg string
}

// Maps a zone name to a list of endpoint subsets all belonging
// to the same service
type ZoneEndpointSubsets map[string][]EndpointSubset

// Maps a service name to a list of endpoint subsets by zone
type ServiceZoneEndpoints map[string]ZoneEndpointSubsets

// Maps a namespace to a list of endpoints by service and zone
type NamespaceServiceEndpoints map[string]ServiceZoneEndpoints

func NewResolverError(em string) *ResolverError {
	return &ResolverError{em}
}

func (e *ResolverError) Error() string {
	return e.errMsg
}

func NewMeshResolver(clientset *kubernetes.Clientset) *MeshResolver {
	return &MeshResolver{clientset}
}

func (zoneEp *ZoneEndpointSubsets) GetSortedZones() []string {
	sortedZones := make([]string, len(*zoneEp))
	idx := 0
	for k, _ := range *zoneEp {
		sortedZones[idx] = k
		idx++
	}
	sort.Strings(sortedZones)
	return sortedZones
}

func (svcEp *ServiceZoneEndpoints) GetSortedServices() []string {
	sortedServices := make([]string, len(*svcEp))
	idx := 0
	for k, _ := range *svcEp {
		sortedServices[idx] = k
		idx++
	}
	sort.Strings(sortedServices)
	return sortedServices
}

func (nsSvcEpss *NamespaceServiceEndpoints) AddEndpointSubsetMap(m *EndpointSubsetMap) {
	for _, epss := range m.GetNamedSubsets() {
		ns := epss.Namespace
		svcZoneEpss, nsExists := (*nsSvcEpss)[ns]
		if !nsExists {
			svcZoneEpss = ServiceZoneEndpoints{}
			(*nsSvcEpss)[ns] = svcZoneEpss
		}
		zoneEpss, svcExists := svcZoneEpss[epss.Service]
		if !svcExists {
			zoneEpss = ZoneEndpointSubsets{}
			svcZoneEpss[epss.Service] = zoneEpss
		}
		epssList, zoneExists := zoneEpss[epss.Zone]
		if !zoneExists {
			epssList = []EndpointSubset{}
			zoneEpss[epss.Zone] = epssList
		}
		zoneEpss[epss.Zone] = append(epssList, *epss)
	}
}

func (nsEp *NamespaceServiceEndpoints) GetSortedNamespaces() []string {
	sortedNamespaces := make([]string, len(*nsEp))
	idx := 0
	for k, _ := range *nsEp {
		sortedNamespaces[idx] = k
		idx++
	}
	sort.Strings(sortedNamespaces)
	return sortedNamespaces
}

// Uses an IP address to reverse lookup a 2 level map: namespace to service to list of EndpointSubsets
func (r *MeshResolver) LookupSvcEndpointsByIp(ipAddress string, labels map[string]string) (nsSvcEpss NamespaceServiceEndpoints, ns, svc *string, err error) {
	opts := v1.ListOptions{}
	opts.LabelSelector = strings.Join([]string{LabelMeshDnsIp, ipAddress}, "=")
	l, err := r.clientset.CoreV1().Endpoints("").List(opts)
	if err != nil {
		return nsSvcEpss, nil, nil, err
	}
	nsSvcEpss = NamespaceServiceEndpoints{}
	for _, ep := range l.Items {
		ns := ep.Namespace
		if ns == "" {
			ns = v1.NamespaceDefault
		}
		nsSvcEpss, err = r.LookupSvcEndpointsBySvcName(ns, ep.Name, labels)
		return nsSvcEpss, &ns, &ep.Name, nil
	}
	return nsSvcEpss, nil, nil, nil
}

func dnsNamesToLables(dnsName string, nvPairs *[]string) (string, error) {
	if dnsName == "" {
		return "", nil
	}
	dnsSubdomains := strings.Split(dnsName, ".")
	// Kubernetes service naming format: _<portname>._<protocol>.my-service.my-namespace.svc.cluster.local
	portProtocolDomains := []string{}
	serviceNameDomains := []string{}
	for _, sd := range dnsSubdomains {
		if strings.HasPrefix(sd, "_") {
			switch {
			case len(serviceNameDomains) > 0:
				return "", NewResolverError(fmt.Sprintf("Incorrect DNS format. Expecting: '%s', found: '%s'", k8sServiceDomainNameFormat, dnsName))
			case len(portProtocolDomains) >= 2:
				return "", NewResolverError(fmt.Sprintf("Incorrect DNS format. Expecting: '%s', found: '%s'", k8sServiceDomainNameFormat, dnsName))
			}
			portProtocolDomains = append(portProtocolDomains, strings.TrimPrefix(sd, "_"))
		} else {
			if len(serviceNameDomains) >= 5 {
				return "", NewResolverError(fmt.Sprintf("Incorrect DNS format. Expecting: '%s', found: '%s'", k8sServiceDomainNameFormat, dnsName))
			}
			serviceNameDomains = append(serviceNameDomains, sd)
		}
	}
	if len(portProtocolDomains) > 1 {
		*nvPairs = append(*nvPairs, strings.Join([]string{LabelPort, portProtocolDomains[0]}, "="))
	}
	if len(serviceNameDomains) == 0 {
		return "", NewResolverError(fmt.Sprintf("Incorrect DNS format. Expecting: '%s', found: '%s'", k8sServiceDomainNameFormat, dnsName))
	}
	*nvPairs = append(*nvPairs, strings.Join([]string{LabelService, serviceNameDomains[0]}, "="))
	if len(serviceNameDomains) > 1 {
		return serviceNameDomains[1], nil
	}
	return "", nil
}

func (r *MeshResolver) LookupSvcEndpointsByDns(dnsName string, labels map[string]string) (keyToEpssMap EndpointSubsetMap, err error) {
	nameValuePairs := []string{strings.Join([]string{LabelMeshEndpoint, "true"}, "=")}
	for k, v := range labels {
		nameValuePairs = append(nameValuePairs, strings.Join([]string{k, v}, "="))
	}
	ns, err := dnsNamesToLables(dnsName, &nameValuePairs)
	if err != nil {
		return keyToEpssMap, err
	}
	opts := v1.ListOptions{}
	opts.LabelSelector = strings.Join(nameValuePairs, ",")
	l, err := r.clientset.CoreV1().Endpoints(ns).List(opts)
	if err != nil {
		return keyToEpssMap, NewResolverError(fmt.Sprintf("Error fetching endpoints for dnsName '%s': %s", dnsName, err.Error()))
	}
	keyToEpssMap = *NewEndpointSubsetMap()
	BuildEndpointSubsetMapFromList(l, &keyToEpssMap)
	return keyToEpssMap, nil
}

func (r *MeshResolver) LookupSvcEndpointsBySvcName(ns, serviceName string, labels map[string]string) (nsSvcEpss NamespaceServiceEndpoints, err error) {
	dnsName := serviceName
	if len(serviceName) > 0 {
		if len(ns) > 0 {
			dnsName = serviceName + "." + ns
		}
	}
	keyToEpssMap, err := r.LookupSvcEndpointsByDns(dnsName, labels)
	if err != nil {
		return nsSvcEpss, err
	}
	nsSvcEpss = NamespaceServiceEndpoints{}
	nsSvcEpss.AddEndpointSubsetMap(&keyToEpssMap)
	return nsSvcEpss, nil
}
