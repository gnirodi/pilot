package mesh

import (
	"github.com/golang/glog"
	crv1 "istio.io/pilot/bridge/api/pkg/v1"
)

type MeshSyncAgent struct {
	ssGetter *ServiceEndpointSubsetGetter
}

type ServiceEndpointSubsetGetter interface {
	GetExpectedEndpointSubsets(localZoneName string) EndpointSubsetMap
}

func NewMeshSyncAgent(ssGetter interface{}) *MeshSyncAgent {
	getter, ok := ssGetter.(*ServiceEndpointSubsetGetter)
	if !ok {
		glog.Fatal("Incorrect ssGetter interface. Expecting a ServiceEndpointSubsetGetter")
	}
	return &MeshSyncAgent{getter}
}

func (*MeshSyncAgent) Run() {
	glog.Info("Started Mesh Sync Agent Daemon")
}

func (*MeshSyncAgent) UpdateMesh(meshCrd *crv1.Mesh) {

}
