#!/bin/bash
echo "============================================"
echo "Hope you've already run: glide update"
echo "Hope you've also deleted nested vendor dirs"
echo "find . -name "vendor" -print -exec rm -rf {} \;"
echo "============================================"
echo
echo
pushd istio.io/pilot/bridge
docker build -f ./Dockerfile.grafana -t grafana-server .
popd
docker tag grafana-server gcr.io/istio-multizone-hybrid/grafana-server:live
gcloud docker -- push gcr.io/istio-multizone-hybrid/grafana-server
