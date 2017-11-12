/*
Copyright 2017 The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	MeshResourceName     = "meshs.config.istio.io"
	MeshSpecGroup        = "config.istio.io"
	MeshSpecVersion      = "v1"
	MeshResourcePlural   = "meshs"
	MeshResourceSingular = "mesh"
	MeshResourceKind     = "Mesh"
	MeshShortName        = "me"
)

// +genclient
// +genclient:noStatus
// +genclient:nonNamespaced

// +k8s:deepcopy-gen=true,register=k8s.io/apimachinery/pkg/runtime.Object
type Mesh struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              MeshSpec   `json:"spec"`
	Status            MeshStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen=true,register=k8s.io/apimachinery/pkg/runtime.Object
type ZoneSpec struct {
	metav1.ObjectMeta `json:"metadata"`
	ZoneName          string `json:"zoneName"`
	MeshSyncAgentVip  string `json:"meshSyncAgentVip"`
	Activate          bool   `json:"activate"`
}

// +k8s:deepcopy-gen=true,register=k8s.io/apimachinery/pkg/runtime.Object
type MeshSpec struct {
	Zones []ZoneSpec `json:"zones"`
}

type MeshStatus struct {
	State   MeshState `json:"state,omitempty"`
	Message string    `json:"message,omitempty"`
}

type MeshState string

const (
	MeshStateCreated   MeshState = "Created"
	MeshStateProcessed MeshState = "Processed"
)

// +k8s:deepcopy-gen=true,register=k8s.io/apimachinery/pkg/runtime.Object
type MeshList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []Mesh `json:"items"`
}

func (in *MeshList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	} else {
		return nil
	}
}

func (in *Mesh) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	} else {
		return nil
	}
}
