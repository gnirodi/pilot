package mesh

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"sort"
	"strings"
)

const (
	KeySeparator = "$$"
)

// Endpoint representation within a Mesh is a single unique svc ip:port combination
type Endpoint struct {
	// The key of the Endpoint is expected to be unique within a given namespace of the local zone
	Key            string
	Namespace      string
	Service        string
	PodIP          string
	SrvPort        string
	Port           v1.ContainerPort
	KeyLabelSuffix string
	PodLabels      map[string]string
}

// Maps an endpoint key to an Endpoint
type KeyEndpointMap map[string]Endpoint

// Maps a zone name to a KeyEndpointMap
type ZoneKeyEndpointMap map[string]KeyEndpointMap

// A mesh EndpointSubset is a unique collection of mesh Endpoints for the tuples of service SrvPort zone and other labels
// It corresponds to exactly one v1.Endpoint which will typically have only one subset
type EndpointSubset struct {
	// The human readable key for the subset
	Key string
	// The k8s endpoint object name unique for the zone
	Name           string
	Namespace      string
	Service        string
	Zone           string
	Labels         map[string]string
	KeyEndpointMap KeyEndpointMap
}

// Maps an uncompresssed EndpointSubset name to the corresponding Endpoint subset
type EndpointSubsetMap struct {
	epSubset  map[string]EndpointSubset
	epNameSet map[string]bool
}

func NewEndpointSubsetMap() *EndpointSubsetMap {
	return &EndpointSubsetMap{map[string]EndpointSubset{}, map[string]bool{}}
}

func NewEndpoint(ns, svc, ip string, port v1.ContainerPort, pl map[string]string) *Endpoint {
	ep := Endpoint{"", ns, svc, ip, "", port, "", map[string]string{}}
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
}

func (ep *Endpoint) ComputeKeyForSortedLabels() {
	ep.Key = strings.Join([]string{ep.Service, ep.Namespace, ep.PodIP, ep.SrvPort, ep.KeyLabelSuffix}, KeySeparator)
}

func (ep *Endpoint) DeepCopy() Endpoint {
	cp := Endpoint{ep.Key, ep.Namespace, ep.Service, ep.PodIP, ep.SrvPort, ep.Port, ep.KeyLabelSuffix, make(map[string]string, len(ep.PodLabels))}
	for k, v := range ep.PodLabels {
		cp.PodLabels[k] = v
	}
	return cp
}

func NewEndpointSubset(key, ns, svc, zone string, lbls map[string]string) *EndpointSubset {
	ss := EndpointSubset{}
	ss.Key = key
	ss.Namespace = ns
	ss.Service = svc
	ss.Zone = zone
	ss.Labels = lbls
	ss.KeyEndpointMap = KeyEndpointMap{}
	return &ss
}

func (es *EndpointSubset) ComputeName() {
	es.Name = fmt.Sprintf("%x", sha256.Sum256([]byte(es.Key)))
	glog.Infof("Compressed bytes from %d to %d", len(es.Key), len(es.Name))
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

func (m *EndpointSubsetMap) AddEndpoint(t *Endpoint, s string, z string) {
	ep := t.DeepCopy()
	ep.Service = s
	ep.ComputeKeyForSortedLabels()
	subsetKey := strings.Join([]string{ep.Service, ep.Namespace, z, ep.SrvPort, ep.KeyLabelSuffix}, KeySeparator)
	ss, ssFound := m.epSubset[subsetKey]
	if !ssFound {
		ss = *NewEndpointSubset(subsetKey, ep.Namespace, s, z, ep.PodLabels)
		m.EnsureUniqueName(&ss)
		m.epSubset[subsetKey] = ss
	}
	ss.KeyEndpointMap[ep.Key] = ep
}
