package main

import (
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

const (
	EnvVarNodeName          = "MY_NODE_NAME"
	EnvVarPodName           = "MY_POD_NAME"
	EnvVarPodNamespace      = "MY_POD_NAMESPACE"
	EnvVarPodIP             = "MY_POD_IP"
	EnvVarPodServiceAccount = "MY_POD_SERVICE_ACCOUNT"
	ServerStatus            = "MY_SERVER_STATUS"
	StatusHeader            = "STATUS_HEADER"
	ServerStatusHeader      = "Istio Hybrid Multi-Zone Mesh Status"
)

var templateDir = flag.String("template_dir", "data/templates", "Root path for HTML templates")
var httpPort = flag.String("http_port", "8080", "Port for serving http traffic")

type StatuszInfo struct {
	ProcInfo              *map[string]string
	TargetUrl             *string
	TargetHealthzResponse *string
}

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
	fmt.Printf("Istio Service Mesh Sync Agent.\n")
	flag.VisitAll(func(f *flag.Flag) {
		fmt.Printf("--%-15.15s:%s\n", f.Name, f.Value.String())
	})

	pi := make(map[string]string)
	buildStatus(&pi, ServerStatusHeader)
	si := StatuszInfo{&pi, nil, nil}

	// Handle all templates
	pattern := filepath.Join(*templateDir, "*")
	tmpl := template.Must(template.ParseGlob(pattern))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t := r.FormValue("targetserver")
		var targetResp string
		if len(t) > 0 {
			if resp, err := http.Get("http://" + t + "/healthz.html"); err == nil {
				defer resp.Body.Close()
				if body, err := ioutil.ReadAll(resp.Body); err == nil {
					targetResp = string(body)
					si.TargetUrl = &t
					si.TargetHealthzResponse = &targetResp
				} else {
					targetResp = err.Error()
					si.TargetUrl = &t
					si.TargetHealthzResponse = &targetResp
				}
			} else {
				targetResp = err.Error()
				si.TargetHealthzResponse = &targetResp
			}
		} else {
			si.TargetUrl = nil
			si.TargetHealthzResponse = nil
		}
		err := tmpl.ExecuteTemplate(w, "statusz.html", &si)
		if err != nil {
			fmt.Printf("Error in statusz.html:\n%s", err.Error())
		}
	})
	http.HandleFunc("/healthz.html", func(w http.ResponseWriter, r *http.Request) {
		err := tmpl.ExecuteTemplate(w, "healthz.html", &pi)
		if err != nil {
			fmt.Printf("Error in statusz.css:\n%s", err.Error())
		}
	})
	http.HandleFunc("/statusz.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(*templateDir, "/statusz.css"))
	})

	go http.ListenAndServe(":"+*httpPort, nil)

	// Start syncing state
	var kubeconfig *string
	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// Assume the MSA is running within a pod
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		// use the current context in kubeconfig (dev / raw VM)
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err.Error())
		}
	}
	// create the clientset from the config
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	for {
		// TODO namespace must be config derived
		services, err := clientset.CoreV1().Services("default").List(metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}
		fmt.Printf("\n-------------------------------\nThere are %d services in the cluster\n", len(services.Items))
		for _, service := range services.Items {
			fmt.Printf("Service: %s\n", service.GetName())
		}

		time.Sleep(10 * time.Second)
	}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
