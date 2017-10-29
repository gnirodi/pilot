package main

import (
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	meshv1 "istio.io/pilot/bridge/clientset/v1"
	"istio.io/pilot/bridge/controllers"
	"istio.io/pilot/bridge/mesh"
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
var nsIgnoreRegex = flag.String("namespace_ignore_list", "kube-system", "Regex of namespaces that need to be ignored by this agent")

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

	mshClientset, err := meshv1.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	podHandler := new(controllers.PodHandler)
	podController := controllers.CreateController(clientset, podHandler)

	svcHandler := new(controllers.ServiceHandler)
	svcController := controllers.CreateController(clientset, svcHandler)

	mshHandler := new(controllers.MeshHandler)
	mshController := controllers.CreateCrdController(mshClientset, mshHandler)

	// Now let's start the controllers
	stop := make(chan struct{})
	defer close(stop)

	go podController.Run(1, stop)
	go svcController.Run(1, stop)
	go mshController.Run(1, stop)

	sl := mesh.NewServiceList(*nsIgnoreRegex)
	fmt.Printf("%v", sl)

	// Wait forever
	select {}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
