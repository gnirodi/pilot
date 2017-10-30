package controllers

import (
	"fmt"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type PodUpdator interface {
	UpdatePod(key string, pod *v1.Pod)
}

type PodHandler struct {
	podUpdator *PodUpdator
}

func NewPodHandler(podUpdator *PodUpdator) *PodHandler {
	podHandler := PodHandler{podUpdator}
	return &podHandler
}

func (p *PodHandler) GetTypeNamePlural() string {
	return "pods"
}

func (p *PodHandler) GetObjectType() runtime.Object {
	return &v1.Pod{}
}

func (p *PodHandler) Handle(key string, obj interface{}) {
	if obj == nil {
		fmt.Printf("Pod %s does not exist anymore\n", key)
		(*p.podUpdator).UpdatePod(key, nil)
	} else {
		pod := obj.(*v1.Pod)
		fmt.Printf("Sync/Add/Update for Pod '%s' from Pod IP '%s' Namespace '%s'\n", pod.GetName(), pod.Status.PodIP, pod.GetNamespace())
		(*p.podUpdator).UpdatePod(key, pod)
	}
}
