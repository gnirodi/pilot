#!/bin/bash
go build demo-app-server.go
docker build -t demo-app-server .
docker tag demo-app-server gcr.io/istio-multizone-hybrid/demo-app-server:live
gcloud docker -- push gcr.io/istio-multizone-hybrid/demo-app-server
