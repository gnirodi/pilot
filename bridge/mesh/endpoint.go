package mesh

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	// "github.com/golang/glog"
	"k8s.io/api/core/v1"
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
	Key            string
	Name           string
	Namespace      string
	Service        string
	Zone           string
	Labels         map[string]string
	KeyEndpointMap KeyEndpointMap
}

// Maps an uncompresssed EndpointSubset name to the corresponding Endpoint subset
type EndpointSubsetMap map[string]EndpointSubset

func NewEndpoint(ns, svc, ip string, port v1.ContainerPort, pl map[string]string) *Endpoint {
	ep := Endpoint{"", ns, svc, ip, "", port, "", map[string]string{}}
	lblIdx := 0
	sortedLabelNames := []string{}
	for k, v := range pl {
		sortedLabelNames[lblIdx] = k
		ep.PodLabels[k] = v
		lblIdx++
	}
	sort.Strings(sortedLabelNames)
	kvp := []string{}
	for _, k := range sortedLabelNames {
		v, _ := ep.PodLabels[k]
		kvp = append(kvp, strings.Join([]string{k, v}, "="))
	}
	ep.KeyLabelSuffix = strings.Join(kvp, KeySeparator)
	ep.SrvPort = port.Name
	portNum := strconv.FormatInt(int64(port.HostPort), 64)
	if ep.SrvPort == "" {
		ep.SrvPort = portNum
	}
	ep.ComputeKeyForSortedLabels()
	return &ep
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
	ss.ComputeName()
	return &ss
}

func (es *EndpointSubset) ComputeName() {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	defer w.Close()
	w.Write([]byte(es.Key))
	es.Name = hex.EncodeToString(b.Bytes())
}

func (m *EndpointSubsetMap) AddEndpoint(t *Endpoint, s string, z string) {
	ep := t.DeepCopy()
	ep.Service = s
	ep.ComputeKeyForSortedLabels()
	subsetName := strings.Join([]string{ep.Service, ep.Namespace, z, ep.SrvPort, ep.KeyLabelSuffix}, KeySeparator)
	ss, ssFound := (*m)[subsetName]
	if !ssFound {
		ss = *NewEndpointSubset(subsetName, ep.Namespace, s, z, ep.PodLabels)
	}
	ss.KeyEndpointMap[ep.Key] = ep
}
