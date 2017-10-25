package controllers

import (
	"fmt"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type PodHandler struct{}

func (p *PodHandler) GetTypeNamePlural() string {
	return "pods"
}

func (p *PodHandler) GetObjectType() runtime.Object {
	return &v1.Pod{}
}

func (p *PodHandler) Handle(key string, obj interface{}) {
	if obj == nil {
		fmt.Printf("Pod %s does not exist anymore\n", key)
	} else {
		pod := obj.(*v1.Pod)
		fmt.Printf("Sync/Add/Update for Pod '%s' from Pod IP '%s' Namespace '%s'\n", pod.GetName(), pod.Status.PodIP, pod.GetNamespace())
	}
}
