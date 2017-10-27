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

package fake

import (
	pkg_v1 "istio.io/pilot/bridge/api/pkg/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeMeshs implements MeshInterface
type FakeMeshs struct {
	Fake *FakePkgV1
}

var meshsResource = schema.GroupVersionResource{Group: "pkg", Version: "v1", Resource: "meshs"}

var meshsKind = schema.GroupVersionKind{Group: "pkg", Version: "v1", Kind: "Mesh"}

// Get takes name of the mesh, and returns the corresponding mesh object, and an error if there is any.
func (c *FakeMeshs) Get(name string, options v1.GetOptions) (result *pkg_v1.Mesh, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootGetAction(meshsResource, name), &pkg_v1.Mesh{})
	if obj == nil {
		return nil, err
	}
	return obj.(*pkg_v1.Mesh), err
}

// List takes label and field selectors, and returns the list of Meshs that match those selectors.
func (c *FakeMeshs) List(opts v1.ListOptions) (result *pkg_v1.MeshList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootListAction(meshsResource, meshsKind, opts), &pkg_v1.MeshList{})
	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &pkg_v1.MeshList{}
	for _, item := range obj.(*pkg_v1.MeshList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested meshs.
func (c *FakeMeshs) Watch(opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewRootWatchAction(meshsResource, opts))
}

// Create takes the representation of a mesh and creates it.  Returns the server's representation of the mesh, and an error, if there is any.
func (c *FakeMeshs) Create(mesh *pkg_v1.Mesh) (result *pkg_v1.Mesh, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootCreateAction(meshsResource, mesh), &pkg_v1.Mesh{})
	if obj == nil {
		return nil, err
	}
	return obj.(*pkg_v1.Mesh), err
}

// Update takes the representation of a mesh and updates it. Returns the server's representation of the mesh, and an error, if there is any.
func (c *FakeMeshs) Update(mesh *pkg_v1.Mesh) (result *pkg_v1.Mesh, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootUpdateAction(meshsResource, mesh), &pkg_v1.Mesh{})
	if obj == nil {
		return nil, err
	}
	return obj.(*pkg_v1.Mesh), err
}

// Delete takes name of the mesh and deletes it. Returns an error if one occurs.
func (c *FakeMeshs) Delete(name string, options *v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewRootDeleteAction(meshsResource, name), &pkg_v1.Mesh{})
	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeMeshs) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	action := testing.NewRootDeleteCollectionAction(meshsResource, listOptions)

	_, err := c.Fake.Invokes(action, &pkg_v1.MeshList{})
	return err
}

// Patch applies the patch and returns the patched mesh.
func (c *FakeMeshs) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *pkg_v1.Mesh, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewRootPatchSubresourceAction(meshsResource, name, data, subresources...), &pkg_v1.Mesh{})
	if obj == nil {
		return nil, err
	}
	return obj.(*pkg_v1.Mesh), err
}
