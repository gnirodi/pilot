package mesh

import (
	"encoding/json"
	"regexp"
	"sync"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
)

type ZoneType int

const (
	LocalSvcAnnotation    = "config.istio.io/mesh.deployment-selector"
	ExternalSvcAnnotation = "config.istio.io/mesh.zone"
	UnknownZone           = ZoneType(0)
	LocalZone             = ZoneType(1)
	ExternalZone          = ZoneType(2)
)

type Service struct {
	Key         string
	Name        string
	Namespace   string
	Labels      map[string]string
	Type        ZoneType
	ConfigDirty bool
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
		if !laFound && !eaFound {
			return
		}
		if laFound && eaFound {
			glog.Errorf("Service named '%s' from namespace '%s' has conficting annotions '%s=%s' and '%s'='%s'. Only one or the other ought to be provided. Ignoring service update.",
				svc.Name, ns, LocalSvcAnnotation, lclAnnot, ExternalSvcAnnotation, extAnnot)
		}
		ms, oldExists := l.serviceMap[key]
		if oldExists {
			switch {
			case ms.Type == ExternalZone && laFound:
				ms.Type = LocalZone
				glog.Infof("External service named '%s' from namespace '%s' was updated with annotations '%s=%s'. Local endpoints will shortly be created",
					svc.Name, ns, LocalSvcAnnotation, lclAnnot)
				break
			case ms.Type == LocalZone && eaFound:
				glog.Infof("External service named '%s' from namespace '%s' was updated with annotations '%s=%s'. Local endpoints will shortly be deleted",
					svc.Name, ns, ExternalSvcAnnotation, extAnnot)
				ms.Type = ExternalZone
				ms.ConfigDirty = false
				break
			case ms.Type == ExternalZone && eaFound:
				ms.ConfigDirty = false
				glog.V(2).Infof("Ignoring Service named '%s' from namespace '%s' in response to external service creation", svc.Name, ns)
				return
			}
		} else {
			ztyp := UnknownZone
			lblMap := map[string]string{}
			if laFound {
				ztyp = LocalZone
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
			} else {
				// May have been created externally or by another local MSA
				ztyp = ExternalZone
			}
			ms = Service{key, svc.Name, svc.Namespace, lblMap, ztyp, true}
			l.serviceMap[key] = ms
		}
	} else {
		delete(l.serviceMap, key)
	}
}
