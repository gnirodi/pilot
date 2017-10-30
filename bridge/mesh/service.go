package mesh

import (
	"encoding/json"
	"regexp"
	"sync"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
)

type MeshServiceType int

const (
	MeshSvcAnnotation = "config.istio.io/mesh.deployment-selector"
	UnknownZone       = MeshServiceType(0)
	MeshService       = MeshServiceType(1)
	NonMeshService    = MeshServiceType(2)
)

type Service struct {
	Key       string
	Name      string
	Namespace string
	SvcType   MeshServiceType
	Labels    map[string]string
}

type ServiceList struct {
	nsIgnoreRegex *regexp.Regexp
	serviceMap    map[string]Service
	mu            sync.RWMutex
	ssGetter      *PodEndpointSubsetGetter
}

type PodEndpointSubsetGetter interface {
	GetExpectedEndpointSubsets(localZoneName string, keySvcMap *map[string]Service) EndpointSubsetMap
}

func NewServiceList(nsIgnoreRegex string, ssGetter interface{}) *ServiceList {
	regex, err := regexp.Compile(nsIgnoreRegex)
	if err != nil {
		glog.Fatal("Error compiling Namespace exclude regex")
	}
	getter, ok := ssGetter.(*PodEndpointSubsetGetter)
	if !ok {
		glog.Fatal("Incorrect ssGetter interface. Expecting a PodEndpointSubsetGetter")
	}

	return &ServiceList{regex, map[string]Service{}, sync.RWMutex{}, getter}
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
			var mf interface{}
			b := []byte{}
			b = append(b, meshAnnot...)
			err := json.Unmarshal(b, &mf)
			if err != nil {
				glog.Errorf("Service named '%s' from namespace '%s' has malformed annotion '%s=%s' Ignoring service update. %s",
					svc.Name, ns, MeshSvcAnnotation, meshAnnot, err.Error())
				maFound = false
			}
			maLabels = mf.(map[string]string)
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
		}
		ms := Service{key, svc.Name, svc.Namespace, svcType, maLabels}
		l.serviceMap[key] = ms
	} else {
		// Deletion event
		delete(l.serviceMap, key)
	}
}

func (l *ServiceList) GetExpectedEndpointSubsets(localZoneName string) EndpointSubsetMap {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return (*l.ssGetter).GetExpectedEndpointSubsets(localZoneName, &l.serviceMap)
}
