#!/bin/bash
echo "Hope you've already run: dep ensure"
go build bridge.go
docker build -t bridge .
docker tag bridge gcr.io/istio-hybrid/bridge:live
gcloud docker -- push gcr.io/istio-hybrid/bridge
