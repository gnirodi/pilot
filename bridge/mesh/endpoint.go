package mesh

import (
	"sort"
	"strconv"
	"strings"
	// "github.com/golang/glog"
	"k8s.io/api/core/v1"
)

const (
	KeySeparator = "$$"
)

// Endpoint representation within a Mesh
type Endpoint struct {
	Key          string
	Namespace    string
	Service      string
	PodIP        string
	Port         v1.ContainerPort
	PodLabelKeys []string
	PodLabels    map[string]string
}

func NewEndpoint(ns, svc, ip string, port v1.ContainerPort, pl map[string]string) *Endpoint {
	ep := Endpoint{"", ns, svc, ip, port, []string{}, map[string]string{}}
	lblIdx := 0
	for k, v := range pl {
		ep.PodLabelKeys[lblIdx] = k
		ep.PodLabels[k] = v
	}
	sort.Strings(ep.PodLabelKeys)
	finalKey := make([]string, len(ep.PodLabelKeys)+1)
	finalKey[0] = strings.Join([]string{ns, svc, ip, strconv.FormatInt(int64(port.HostPort), 64)}, KeySeparator)
	for i, k := range ep.PodLabelKeys {
		v, _ := ep.PodLabels[k]
		kvp := strings.Join([]string{k, v}, "=")
		finalKey[i+1] = kvp
	}
	ep.Key = strings.Join(finalKey, KeySeparator)
	return &ep
}

func (ep *Endpoint) DeepCopy() Endpoint {
	cp := Endpoint{ep.Key, ep.Namespace, ep.Service, ep.PodIP, ep.Port, make([]string, len(ep.PodLabelKeys)), make(map[string]string, len(ep.PodLabelKeys))}
	for i, k := range ep.PodLabelKeys {
		cp.PodLabelKeys[i] = k
	}
	for k, v := range ep.PodLabels {
		cp.PodLabels[k] = v
	}
	return cp
}

//type ZoneSet map[string]bool
//
//type EndpointSubset struct {
//	Key             string
//	EndpointPort    []v1.EndpointPort
//	EndpointAddress []v1.EndpointAddress
//}
//
//type ServiceEndpoints struct {
//	Service        string
//	Labels         map[string]string
//	EndpointSubset []EndpointSubset
//}
//
//type ZoneServiceEndpoints struct {
//	Zone             string
//	ServiceEndpoints map[string]ServiceEndpoints
//}
//
//type Endpoints struct {
//	ZoneServiceEndpoints map[string]ZoneServiceEndpoints
//}
