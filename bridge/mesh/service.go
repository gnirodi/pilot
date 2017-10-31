package mesh

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
)

type MeshServiceType int

const (
	MeshSvcAnnotation   = "config.istio.io/mesh.deployment-selector"
	MeshAgentAnnotation = "config.istio.io/mesh.agent"
	UnknownZone         = MeshServiceType(0)
	MeshService         = MeshServiceType(1)
	NonMeshService      = MeshServiceType(2)
)

type Service struct {
	Key       string
	Name      string
	Namespace string
	SvcType   MeshServiceType
	Labels    map[string]string
	agentVip  string
}

type ServiceList struct {
	nsIgnoreRegex *regexp.Regexp
	serviceMap    map[string]Service
	agentVips     map[string]bool
	ssGetter      PodEndpointSubsetGetter
	mu            sync.RWMutex
}

type PodEndpointSubsetGetter interface {
	GetExpectedEndpointSubsets(localZoneName string, keySvcMap *map[string]Service) EndpointSubsetMap
}

func NewServiceList(nsIgnoreRegex string, ssGetter PodEndpointSubsetGetter) *ServiceList {
	regex, err := regexp.Compile(nsIgnoreRegex)
	if err != nil {
		glog.Fatal("Error compiling Namespace exclude regex")
	}
	return &ServiceList{regex, map[string]Service{}, map[string]bool{}, ssGetter, sync.RWMutex{}}
}

func (l *ServiceList) UpdateService(key string, svc *v1.Service) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if svc != nil {
		ns := svc.Namespace
		if ns == "" {
			ns = v1.NamespaceDefault
		}
		if l.nsIgnoreRegex.MatchString(ns) {
			glog.V(2).Infof("Ignoring Service named '%s' from namespace '%s' due to namespace exclude regex '%s'", svc.Name, ns, l.nsIgnoreRegex.String())
			return
		}
		meshAnnot, maFound := svc.Annotations[MeshSvcAnnotation]
		maLabels := map[string]string{}
		if maFound {
			mf := make(map[string]interface{})
			b := []byte{}
			b = append(b, meshAnnot...)
			err := json.Unmarshal(b, &mf)
			if err != nil {
				glog.Errorf("Service named '%s' from namespace '%s' has malformed annotion '%s=%s' Ignoring service update. %s",
					svc.Name, ns, MeshSvcAnnotation, meshAnnot, err.Error())
				maFound = false
			}
			for k, v := range mf {
				val, ok := v.(string)
				if !ok {
					glog.Errorf("Service named '%s' from namespace '%s' has malformed annotion '%s=%s' Ignoring service update. %s",
						svc.Name, ns, MeshSvcAnnotation, meshAnnot, err.Error())
					maFound = false
					break
				}
				maLabels[k] = val
			}
		}

		svLabels := svc.GetLabels()
		hasLabels := len(svLabels) > 0

		var svcType MeshServiceType
		switch {
		case !hasLabels && maFound:
			svcType = MeshService
			break
		case !maFound:
			svcType = NonMeshService
			break
		case hasLabels && maFound:
			allPresent := true
			for km, vm := range maLabels {
				vs, vsFound := svLabels[km]
				switch {
				case !vsFound:
					glog.Errorf("Service named '%s' from namespace '%s' with annotion '%s=%s' has no matching label '%s=%s' in service labels %v. Designating service as Non-Mesh",
						svc.Name, ns, MeshSvcAnnotation, meshAnnot, km, vm, svLabels)
					allPresent = false
					break
				case vm != vs:
					glog.Errorf("Service named '%s' from namespace '%s' with annotion '%s=%s' has no matching label value for '%s=%s' in service labels %v. Designating service as Non-Mesh",
						svc.Name, ns, MeshSvcAnnotation, meshAnnot, km, vm, svLabels)
					allPresent = false
					break
				}
				if !allPresent {
					break
				}
			}
			if allPresent {
				svcType = MeshService
			} else {
				svcType = NonMeshService
			}
			break
		}
		msaAnnot, msaAnnotFound := svc.Annotations[MeshAgentAnnotation]
		msaVip := ""
		if msaAnnotFound && strings.ToLower(msaAnnot) == "true" {
			msaVip = svc.Spec.ClusterIP
			if len(msaVip) > 0 {
				l.agentVips[msaVip] = true
			}
		}
		ms := Service{key, svc.Name, svc.Namespace, svcType, maLabels, msaVip}
		l.serviceMap[key] = ms
	} else {
		// Deletion event
		svc, svcFound := l.serviceMap[key]
		if svcFound {
			if len(svc.agentVip) > 0 {
				delete(l.agentVips, svc.agentVip)
			}
		}
		delete(l.serviceMap, key)
	}
}

func (l *ServiceList) GetExpectedEndpointSubsets(localZoneName string) EndpointSubsetMap {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.ssGetter.GetExpectedEndpointSubsets(localZoneName, &l.serviceMap)
}

func (l *ServiceList) GetAgentVips() map[string]bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.agentVips
}
