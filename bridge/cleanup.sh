#!/bin/bash
ksc z1
kubectl delete -f z1-msa.yaml
kubectl delete -f demo-app-server/z1-service.yaml
kubectl delete ep -l config.istio.io/mesh.endpoint=true
kubectl delete mesh demo-mesh
kubectl delete CustomResourceDefinition meshs.config.istio.io
ksc z2
kubectl delete -f z2-msa.yaml
kubectl delete -f demo-app-server/z2-service.yaml
kubectl delete ep -l config.istio.io/mesh.endpoint=true
kubectl delete mesh demo-mesh
kubectl delete CustomResourceDefinition meshs.config.istio.io
