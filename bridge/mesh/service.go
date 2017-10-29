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
	LocalSvcAnnotation    = "config.istio.io/mesh.deployment-selector"
	ExternalSvcAnnotation = "config.istio.io/mesh.zone"
	UnknownZone           = MeshServiceType(0)
	LocalZoneService      = MeshServiceType(1)
	ExternalZoneService   = MeshServiceType(2)
	NonMeshService        = MeshServiceType(3)
)

type Service struct {
	Key              string
	Name             string
	Namespace        string
	Labels           map[string]string
	Type             MeshServiceType
	ConfigDirty      bool
	localAnnotation  string
	localEndpointMap KeyEndpointMap
	zoneEndpointMap  ZoneKeyEndpointMap
	EndpointsDirty   bool
}

type ServiceList struct {
	nsIgnoreRegex *regexp.Regexp
	serviceMap    map[string]Service
	mu            sync.RWMutex
}

func NewServiceList(nsIgnoreRegex string) *ServiceList {
	regex, err := regexp.Compile(nsIgnoreRegex)
	if err != nil {
		glog.Fatal("Error compiling Namespace exclude regex")
	}

	return &ServiceList{regex, map[string]Service{}, sync.RWMutex{}}
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
		lclAnnot, laFound := svc.Annotations[LocalSvcAnnotation]
		extAnnot, eaFound := svc.Annotations[ExternalSvcAnnotation]
		if laFound && eaFound {
			glog.Errorf("Service named '%s' from namespace '%s' has conficting annotion '%s=%s' and '%s'='%s'. Only one or the other ought to be provided. Ignoring service update.",
				svc.Name, ns, LocalSvcAnnotation, lclAnnot, ExternalSvcAnnotation, extAnnot)
		}
		ms, oldExists := l.serviceMap[key]
		if oldExists {
			switch {
			case ms.Type == ExternalZoneService && laFound:
				ms.Type = LocalZoneService
				glog.Infof("External service named '%s' from namespace '%s' was updated with annotation '%s=%s'. Local endpoints will shortly be created",
					svc.Name, ns, LocalSvcAnnotation, lclAnnot)
				break
			case ms.Type == LocalZoneService && eaFound:
				glog.Infof("Local service named '%s' from namespace '%s' was updated with annotation '%s=%s'. Local endpoints will shortly be deleted",
					svc.Name, ns, ExternalSvcAnnotation, extAnnot)
				ms.Type = ExternalZoneService
				ms.ConfigDirty = false
				ms.localAnnotation = ""
				ms.Labels = map[string]string{}
				if len(ms.localEndpointMap) > 0 {
					ms.localEndpointMap = KeyEndpointMap{}
					ms.EndpointsDirty = true
				}
				return
			case ms.Type == ExternalZoneService && eaFound:
				if ms.ConfigDirty {
					glog.V(2).Infof("Acknowledging creation of external service with name '%s' from namespace '%s'", svc.Name, ns)
					ms.ConfigDirty = false
				} else {
					glog.V(2).Infof("Ignoring creation of external service with name '%s' from namespace '%s'. This service was already noted as external", svc.Name, ns)
				}
				return
			case ms.Type == LocalZoneService && laFound:
				if ms.localAnnotation != lclAnnot && len(ms.localEndpointMap) > 0 {
					glog.Infof("Acknowledging update of local service with name '%s' from namespace '%s'. Annotation '%s' changed from '%s' to '%s'",
						svc.Name, ns, LocalSvcAnnotation, ms.localAnnotation, lclAnnot)
					ms.localEndpointMap = KeyEndpointMap{}
					ms.EndpointsDirty = true
				} else {
					glog.V(2).Infof("Ignoring creation of local service with name '%s' from namespace '%s' with no changes in annotation '%s=%s'",
						svc.Name, ns, LocalSvcAnnotation, ms.localAnnotation)
					return
				}
			case !laFound && !eaFound:
				glog.Infof("Service named '%s' from namespace '%s' was updated with none of the annotations required for mesh operation: '%s', '%s'. Removing service from Mesh",
					svc.Name, ns, LocalSvcAnnotation, ExternalSvcAnnotation)
				ms.localAnnotation = ""
				ms.Labels = map[string]string{}
				ms.localEndpointMap = KeyEndpointMap{}
				ms.zoneEndpointMap = ZoneKeyEndpointMap{}
				ms.Type = NonMeshService
				ms.EndpointsDirty = true
				return
			}
		}
		ztyp := UnknownZone // Used only for new service creation
		lblMap := map[string]string{}
		if laFound {
			ztyp = LocalZoneService
			var mf interface{}
			b := []byte{}
			b = append(b, lclAnnot...)
			err := json.Unmarshal(b, &mf)
			if err != nil {
				glog.Errorf("Service named '%s' from namespace '%s' has malformed annotion '%s=%s' Ignoring service update. %s",
					svc.Name, ns, LocalSvcAnnotation, lclAnnot, err.Error())
				return
			}
			lblMap = mf.(map[string]string)
			if oldExists {
				glog.Infof("Service named '%s' from namespace '%s' has updated annotion '%s=%s' Local endpoints will shortly be updated",
					svc.Name, ns, LocalSvcAnnotation, lclAnnot)
				ms.Labels = lblMap
				return
			}
		} else if eaFound {
			// May have been created externally or by another local MSA
			ztyp = ExternalZoneService
		}
		// If !oldExists
		ms = Service{key, svc.Name, svc.Namespace, lblMap, ztyp, true, lclAnnot, KeyEndpointMap{}, ZoneKeyEndpointMap{}, false}
		l.serviceMap[key] = ms
	} else {
		// Deletion event
		ms, oldExists := l.serviceMap[key]
		if oldExists {
			glog.Infof("Mesh service named '%s' from namespace '%s' was deleted'. Mesh endpoints will shortly be deleted",
				ms.Name, ms.Namespace)
			delete(l.serviceMap, key)
		}
	}
}
