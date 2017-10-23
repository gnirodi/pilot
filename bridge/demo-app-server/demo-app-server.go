package main

import (
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
)

const (
	EnvVarNodeName          = "MY_NODE_NAME"
	EnvVarPodName           = "MY_POD_NAME"
	EnvVarPodNamespace      = "MY_POD_NAMESPACE"
	EnvVarPodIP             = "MY_POD_IP"
	EnvVarPodServiceAccount = "MY_POD_SERVICE_ACCOUNT"
	ServerStatus            = "MY_SERVER_STATUS"
	StatusHeader            = "STATUS_HEADER"
	ServerStatusHeader      = "Demo App Server Status"
)

var docRoot = flag.String("template_dir", "data/templates", "Root for http templates")
var httpPort = flag.String("http_port", "8080", "Port for serving http traffic")

func buildStatus(m *map[string]string, statusHeader string) {
	(*m)[StatusHeader] = statusHeader
	(*m)[EnvVarNodeName] = os.Getenv(EnvVarNodeName)
	(*m)[EnvVarPodName] = os.Getenv(EnvVarPodName)
	(*m)[EnvVarPodNamespace] = os.Getenv(EnvVarPodNamespace)
	(*m)[EnvVarPodIP] = os.Getenv(EnvVarPodIP)
	(*m)[EnvVarPodServiceAccount] = os.Getenv(EnvVarPodServiceAccount)
	(*m)[ServerStatus] = "OK"
}

func main() {
	flag.Parse()
	fmt.Printf("Demo App Server.\n")
	flag.VisitAll(func(f *flag.Flag) {
		fmt.Printf("--%-15.15s:%s\n", f.Name, f.Value.String())
	})

	var serverStatus map[string]string = make(map[string]string)
	buildStatus(&serverStatus, ServerStatusHeader)

	// Handle all templates
	pattern := filepath.Join(*docRoot, "*.html")
	tmpl := template.Must(template.ParseGlob(pattern))
	http.HandleFunc("/healthz.html", func(w http.ResponseWriter, r *http.Request) {
		err := tmpl.ExecuteTemplate(w, "healthz.html", &serverStatus)
		if err != nil {
			fmt.Printf("Error in statusz.html:\n%s", err.Error())
		}
	})

	http.ListenAndServe(":"+*httpPort, nil)
}
