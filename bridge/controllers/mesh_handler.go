package controllers

import (
	"fmt"

	crv1 "istio.io/pilot/bridge/api/pkg/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type MeshHandler struct{}

func (p *MeshHandler) GetTypeNamePlural() string {
	return "meshs"
}

func (p *MeshHandler) GetObjectType() runtime.Object {
	obj := crv1.Mesh{}
	return &obj
}

func (p *MeshHandler) Handle(key string, obj interface{}) {
	if obj == nil {
		fmt.Printf("Mesh %s does not exist anymore\n", key)
	} else {
		mesh := obj.(*crv1.Mesh)
		fmt.Printf("Sync/Add/Update for Mesh '%s' Namespace '%s'\n", mesh.GetName(), mesh.GetNamespace())
	}
}
