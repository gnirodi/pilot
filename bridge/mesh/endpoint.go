package mesh

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
)

const (
	KeySeparator      = "$$"
	LabelMeshEndpoint = "config.istio.io/mesh.endpoint"
	LabelMeshExternal = "config.istio.io/mesh.external"
	LabelMeshDnsSrv   = "config.istio.io/mesh.dns-ep"
	LabelMeshDnsIp    = "config.istio.io/mesh.dns-ip"
	LabelZone         = "zone"
	LabelService      = "service"
	LabelPort         = "port"
)

// Endpoint representation within a Mesh is a single unique svc ip:port combination
type Endpoint struct {
	// The key of the Endpoint is expected to be unique within a given namespace of the local zone
	// It's a concat of Service, Namespace, Zone, PodIP, SrvPort, All key values of Labels
	Key            string            `json:"key"`
	Namespace      string            `json:"namespace"`
	Service        string            `json:"service"`
	Zone           string            `json:"zone"`
	PodIP          string            `json:"podIP"`
	SrvPort        string            `json:"srvPort"`
	Port           v1.ContainerPort  `json:"port,omitempty"`
	KeyLabelSuffix string            `json:"keyLableSuffix"`
	PodLabels      map[string]string `json:"podLabels"`
}

// Maps an endpoint key to an Endpoint
type KeyEndpointMap map[string]*Endpoint

// A mesh EndpointSubset is a unique collection of mesh Endpoints for the tuples of service SrvPort zone and other labels
// It corresponds to exactly one v1.Endpoint which will typically have only one subset
type EndpointSubset struct {
	// The human readable key for the subset
	// It's a concat of t.Service, t.Namespace, Zone along with
	//		SrvPort, KeyLabelSuffix that are common
	// to all Endpoints in the subset
	Key string `json:"key"`
	// The k8s endpoint object name unique for the zone
	Name           string            `json:"name"`
	Namespace      string            `json:"namespace"`
	Service        string            `json:"service"`
	Zone           string            `json:"zone"`
	SrvPort        string            `json:"srvPort"`
	Labels         map[string]string `json:"labels"`
	KeyEndpointMap KeyEndpointMap    `json:"keyEndpointMap"`
	HostPortSet    map[string]bool   `json:"hostPortMap"`
}

// Maps an uncompresssed EndpointSubset name to the corresponding Endpoint subset
type EndpointSubsetMap struct {
	epSubset  map[string]*EndpointSubset
	epNameSet map[string]bool
}

func NewEndpoint(ns, svc, zone, ip string, port v1.ContainerPort, pl map[string]string) *Endpoint {
	ep := Endpoint{"", ns, svc, zone, ip, "", port, "", map[string]string{}}
	sortedLabelNames := []string{}
	for k, v := range pl {
		sortedLabelNames = append(sortedLabelNames, k)
		ep.PodLabels[k] = v
	}
	sort.Strings(sortedLabelNames)
	kvp := []string{}
	for _, k := range sortedLabelNames {
		v, _ := ep.PodLabels[k]
		kvp = append(kvp, strings.Join([]string{k, v}, "="))
	}
	ep.KeyLabelSuffix = strings.Join(kvp, KeySeparator)
	ep.SetPort(port)
	ep.ComputeKeyForSortedLabels()
	return &ep
}

func (ep *Endpoint) SetPort(port v1.ContainerPort) {
	ep.SrvPort = port.Name
	if ep.SrvPort == "" {
		ep.SrvPort = fmt.Sprintf("%d", port.ContainerPort)
	}
	ep.Port = port
}

func (ep *Endpoint) ComputeKeyForSortedLabels() {
	ep.Key = strings.Join([]string{ep.Namespace, ep.Service, ep.Zone, ep.PodIP, ep.SrvPort, ep.KeyLabelSuffix}, KeySeparator)
}

func (ep *Endpoint) DeepCopy() *Endpoint {
	cp := Endpoint{ep.Key, ep.Namespace, ep.Service, ep.Zone, ep.PodIP, ep.SrvPort, ep.Port, ep.KeyLabelSuffix, make(map[string]string, len(ep.PodLabels))}
	for k, v := range ep.PodLabels {
		cp.PodLabels[k] = v
	}
	return &cp
}

func (t *Endpoint) BuildSubsetKey() string {
	return strings.Join([]string{t.Namespace, t.Service, t.Zone, t.SrvPort, t.KeyLabelSuffix}, KeySeparator)
}

func NewEndpointSubset(key, ns, svc, zone, srvPort string, lbls map[string]string) *EndpointSubset {
	ss := EndpointSubset{key, "", ns, svc, zone, srvPort, lbls, KeyEndpointMap{}, map[string]bool{}}
	ss.ComputeName()
	return &ss
}

func (epss *EndpointSubset) DeepCopy() *EndpointSubset {
	clone := NewEndpointSubset(epss.Key, epss.Namespace, epss.Service, epss.Zone, epss.SrvPort,
		make(map[string]string, len(epss.Labels)))
	for k, v := range epss.Labels {
		clone.Labels[k] = v
	}
	for keyEp, ep := range epss.KeyEndpointMap {
		clone.KeyEndpointMap[keyEp] = ep.DeepCopy()
		epss.HostPortSet[net.JoinHostPort(ep.PodIP, fmt.Sprintf("%d", ep.Port.ContainerPort))] = true
	}
	return clone
}

func (epss *EndpointSubset) GetPort() int32 {
	var port int32
	for _, ep := range epss.KeyEndpointMap {
		port = ep.Port.ContainerPort
		break
	}
	return port
}

func (es *EndpointSubset) ComputeName() {
	es.Name = fmt.Sprintf("%x", sha256.Sum256([]byte(es.Key)))
}

func (epss *EndpointSubset) AddExternalEndpointLabel() {
	epss.Labels[LabelMeshExternal] = "true"
	epss.Key = epss.Key + KeySeparator + LabelMeshExternal + "=true"
	epss.ComputeName()
}

func (eps *EndpointSubset) ToK8sEndpoints() v1.Endpoints {
	vep := v1.Endpoints{}
	vep.SetName(eps.Name)
	vep.SetNamespace(eps.Namespace)
	vep.Subsets = []v1.EndpointSubset{}
	vepLabels := map[string]string{LabelMeshEndpoint: "true", LabelService: eps.Service, LabelZone: eps.Zone}
	portLabelSet := false
	vepss := v1.EndpointSubset{}
	vepss.Ports = []v1.EndpointPort{}
	vepss.Addresses = []v1.EndpointAddress{}
	for _, ep := range eps.KeyEndpointMap {
		if !portLabelSet {
			vepLabels[LabelPort] = ep.SrvPort
			for k, v := range ep.PodLabels {
				vepLabels[k] = v
			}
			vep.SetLabels(vepLabels)
			vepPort := v1.EndpointPort{}
			vepPort.Name = ep.SrvPort
			vepPort.Port = ep.Port.ContainerPort
			vepPort.Protocol = ep.Port.Protocol
			vepss.Ports = append(vepss.Ports, vepPort)
			portLabelSet = true
		}
		vepss.Addresses = append(vepss.Addresses, v1.EndpointAddress{ep.PodIP, "", nil, nil})
	}
	vep.Subsets = append(vep.Subsets, vepss)
	return vep
}

func (m *EndpointSubsetMap) AddEndpoint(template *Endpoint, svc string, zone string) {
	ep := template.DeepCopy()
	ep.Service = svc
	ep.Zone = zone
	ep.ComputeKeyForSortedLabels()
	subsetKey := ep.BuildSubsetKey()
	ss, ssFound := m.epSubset[subsetKey]
	if !ssFound {
		ss = NewEndpointSubset(subsetKey, ep.Namespace, svc, zone, ep.SrvPort, ep.PodLabels)
		m.EnsureUniqueName(ss)
		m.epSubset[subsetKey] = ss
		m.epNameSet[ss.Name] = true
	}
	ss.KeyEndpointMap[ep.Key] = ep
}

func NewEndpointSubsetMap() *EndpointSubsetMap {
	return &EndpointSubsetMap{map[string]*EndpointSubset{}, map[string]bool{}}
}

func (m *EndpointSubsetMap) EnsureUniqueName(eps *EndpointSubset) {
	eps.ComputeName()
	for _, found := m.epNameSet[eps.Name]; found; {
		b := make([]byte, 1)
		_, err := rand.Read(b)
		if err == nil {
			eps.Name = eps.Name + fmt.Sprintf("%x", b)
		}
	}
}

func (m *EndpointSubsetMap) ToJson() []byte {
	emptyReturn := []byte("{}")
	b, err := json.Marshal(m.epSubset)
	if err != nil {
		glog.Warningf("Unable to export endpoints %v", err)
		return emptyReturn
	}
	return b
}

func BuildEndpointSubsetMapFromList(l *v1.EndpointsList, m *EndpointSubsetMap) {
	for _, vep := range l.Items {
		ns := vep.Namespace
		if ns == "" {
			ns = v1.NamespaceDefault
		}
		pl := map[string]string{}
		svc := ""
		zone := ""
		port := ""
		isExternal := false
		for k, v := range vep.Labels {
			switch {
			case k == LabelMeshEndpoint:
				continue
			case k == LabelMeshExternal:
				isExternal = true
				continue
			case k == LabelService:
				svc = v
				continue
			case k == LabelZone:
				zone = v
				continue
			case k == LabelPort:
				port = v
				continue
			}
			pl[k] = v
		}
		eptPort := v1.ContainerPort{}
		templ := NewEndpoint(ns, svc, zone, port, eptPort, pl)
		for _, vs := range vep.Subsets {
			for _, ip := range vs.Addresses {
				ept := templ.DeepCopy()
				ept.PodIP = ip.IP
				if len(vs.Ports) > 0 {
					eptPort.ContainerPort = vs.Ports[0].Port
					eptPort.Name = port
					eptPort.Protocol = vs.Ports[0].Protocol
					ept.SetPort(eptPort)
				}
				ept.ComputeKeyForSortedLabels()
				subsetKey := ept.BuildSubsetKey()
				if isExternal {
					subsetKey = GetActualDnsEpInfoKey(&vep)
				}
				ss, ssFound := m.epSubset[subsetKey]
				if !ssFound {
					ss = NewEndpointSubset(subsetKey, ept.Namespace, svc, zone, port, ept.PodLabels)
					ss.Name = vep.Name
					m.epSubset[subsetKey] = ss
				}
				ss.KeyEndpointMap[ept.Key] = ept
			}
		}
	}
}
