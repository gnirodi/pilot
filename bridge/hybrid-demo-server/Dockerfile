# Step 1: cd <to the directory containing this file>
#
# Step 2: Build the docker image
# docker build -t hybrid-demo-server .
#
# Step 3: Run the docker image
# docker run -p 8080:8080 hybrid-demo-server

# Start from a Debian image with the latest version of Go installed
# The go workspace will be /go.
FROM golang

# Copy the local package files to the container's workspace.
ADD . /go/src/github.com/istio/pilot/bridge/hybrid-demo-server

# Add the web server's doc root.
ADD data /go/data


# Build the webservice application for the container.
# This service has no dependencies for now. But if we add deps
# we'll need to add them here manually or tools like "godep"
RUN go install github.com/istio/pilot/bridge/hybrid-demo-server

# Run the webservice command by default when the container starts.
ENTRYPOINT /go/bin/hybrid-demo-server

# Document that the service listens on port 8080 by default.
EXPOSE 8080
