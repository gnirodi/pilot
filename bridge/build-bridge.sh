#!/bin/bash
echo "Hope you've already run: dep ensure"
pushd istio.io/pilot/bridge
go build istio.io/pilot/bridge
docker build -t bridge .
popd
docker tag bridge gcr.io/istio-hybrid/bridge:live
gcloud docker -- push gcr.io/istio-hybrid/bridge
