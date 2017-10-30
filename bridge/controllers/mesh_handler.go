package controllers

import (
	"sync"

	"github.com/golang/glog"
	crv1 "istio.io/pilot/bridge/api/pkg/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type MeshHandler struct {
	primaryMesh *crv1.Mesh
	meshMap     map[string]*crv1.Mesh
	mu          sync.RWMutex
	configDirty bool
}

func NewMeshHandler() *MeshHandler {
	mh := MeshHandler{nil, map[string]*crv1.Mesh{}, sync.RWMutex{}, false}
	return &mh
}

func (p *MeshHandler) GetTypeNamePlural() string {
	return "meshs"
}

func (p *MeshHandler) GetObjectType() runtime.Object {
	obj := crv1.Mesh{}
	return &obj
}

func (p *MeshHandler) InitMesh(mesh *crv1.Mesh) {
	p.primaryMesh = mesh
	p.configDirty = true
}

func (p *MeshHandler) Handle(key string, obj interface{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if obj == nil {
		glog.Infof("Mesh %s does not exist anymore\n", key)
		delete(p.meshMap, key)
		if len(p.meshMap) == 0 {
			p.primaryMesh = nil
			p.configDirty = true
		}
	} else {
		mesh := obj.(*crv1.Mesh)
		glog.Infof("Sync/Add/Update for Mesh '%s'\n", mesh.GetName(), mesh.GetNamespace())
		if p.primaryMesh != nil {
			if p.primaryMesh.Name != mesh.GetName() {
				glog.Errorf("An environment can only have a single mesh. Primary mesh '%s' is still active. Mesh '%s' ignored\n",
					p.primaryMesh.Name, mesh.GetName())
			} else {
				p.InitMesh(mesh)
			}
		}
		if len(p.meshMap) == 0 {
			p.InitMesh(mesh)
		}
		p.meshMap[key] = mesh
	}
}
