package controllers

import (
	"fmt"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type ServiceUpdator interface {
	UpdateService(key string, svc *v1.Service)
}

type ServiceHandler struct {
	svcUpdator *ServiceUpdator
}

func NewServiceHandler(svcUpdator *ServiceUpdator) *ServiceHandler {
	svcHandler := ServiceHandler{svcUpdator}
	return &svcHandler
}

func (p *ServiceHandler) GetTypeNamePlural() string {
	return "services"
}

func (p *ServiceHandler) GetObjectType() runtime.Object {
	return &v1.Service{}
}

func (p *ServiceHandler) Handle(key string, obj interface{}) {
	if obj == nil {
		fmt.Printf("Service %s does not exist anymore\n", key)
		(*p.svcUpdator).UpdateService(key, nil)
	} else {
		svc := obj.(*v1.Service)
		fmt.Printf("Sync/Add/Update for Service '%s' Namespace '%s'\n", svc.GetName(), svc.GetNamespace())
		(*p.svcUpdator).UpdateService(key, svc)
	}
}
