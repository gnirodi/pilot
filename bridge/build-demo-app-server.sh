#!/bin/bash
echo "============================================"
echo "Hope you've already run: glide update"
echo "Hope you've also deleted nested vendor dirs"
echo "find . -name "vendor" -print -exec rm -rf {} \;"
echo "============================================"
echo
echo
pushd istio.io/pilot/bridge
go build istio.io/pilot/bridge
docker build -f ./Dockerfile.demo-app-server -t demo-app-server .
popd
docker tag demo-app-server gcr.io/istio-multizone-hybrid/demo-app-server:live
gcloud docker -- push gcr.io/istio-multizone-hybrid/demo-app-server
