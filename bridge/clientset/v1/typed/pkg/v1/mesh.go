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
	v1 "istio.io/pilot/bridge/api/pkg/v1"
	scheme "istio.io/pilot/bridge/clientset/v1/scheme"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// MeshsGetter has a method to return a MeshInterface.
// A group's client should implement this interface.
type MeshsGetter interface {
	Meshs() MeshInterface
}

// MeshInterface has methods to work with Mesh resources.
type MeshInterface interface {
	Create(*v1.Mesh) (*v1.Mesh, error)
	Update(*v1.Mesh) (*v1.Mesh, error)
	Delete(name string, options *meta_v1.DeleteOptions) error
	DeleteCollection(options *meta_v1.DeleteOptions, listOptions meta_v1.ListOptions) error
	Get(name string, options meta_v1.GetOptions) (*v1.Mesh, error)
	List(opts meta_v1.ListOptions) (*v1.MeshList, error)
	Watch(opts meta_v1.ListOptions) (watch.Interface, error)
	Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1.Mesh, err error)
	MeshExpansion
}

// meshs implements MeshInterface
type meshs struct {
	client rest.Interface
}

// newMeshs returns a Meshs
func newMeshs(c *PkgV1Client) *meshs {
	return &meshs{
		client: c.RESTClient(),
	}
}

// Get takes name of the mesh, and returns the corresponding mesh object, and an error if there is any.
func (c *meshs) Get(name string, options meta_v1.GetOptions) (result *v1.Mesh, err error) {
	result = &v1.Mesh{}
	err = c.client.Get().
		Resource("meshs").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do().
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of Meshs that match those selectors.
func (c *meshs) List(opts meta_v1.ListOptions) (result *v1.MeshList, err error) {
	result = &v1.MeshList{}
	err = c.client.Get().
		Resource("meshs").
		VersionedParams(&opts, scheme.ParameterCodec).
		Do().
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested meshs.
func (c *meshs) Watch(opts meta_v1.ListOptions) (watch.Interface, error) {
	opts.Watch = true
	return c.client.Get().
		Resource("meshs").
		VersionedParams(&opts, scheme.ParameterCodec).
		Watch()
}

// Create takes the representation of a mesh and creates it.  Returns the server's representation of the mesh, and an error, if there is any.
func (c *meshs) Create(mesh *v1.Mesh) (result *v1.Mesh, err error) {
	result = &v1.Mesh{}
	err = c.client.Post().
		Resource("meshs").
		Body(mesh).
		Do().
		Into(result)
	return
}

// Update takes the representation of a mesh and updates it. Returns the server's representation of the mesh, and an error, if there is any.
func (c *meshs) Update(mesh *v1.Mesh) (result *v1.Mesh, err error) {
	result = &v1.Mesh{}
	err = c.client.Put().
		Resource("meshs").
		Name(mesh.Name).
		Body(mesh).
		Do().
		Into(result)
	return
}

// Delete takes name of the mesh and deletes it. Returns an error if one occurs.
func (c *meshs) Delete(name string, options *meta_v1.DeleteOptions) error {
	return c.client.Delete().
		Resource("meshs").
		Name(name).
		Body(options).
		Do().
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *meshs) DeleteCollection(options *meta_v1.DeleteOptions, listOptions meta_v1.ListOptions) error {
	return c.client.Delete().
		Resource("meshs").
		VersionedParams(&listOptions, scheme.ParameterCodec).
		Body(options).
		Do().
		Error()
}

// Patch applies the patch and returns the patched mesh.
func (c *meshs) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1.Mesh, err error) {
	result = &v1.Mesh{}
	err = c.client.Patch(pt).
		Resource("meshs").
		SubResource(subresources...).
		Name(name).
		Body(data).
		Do().
		Into(result)
	return
}
