package main

import (
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sync"

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
	ProcInfo              *ProcessInfo
	TargetUrl             *string
	TargetHealthzResponse *string
}

type ProcessInfo struct {
	labels map[string]string
	mu     sync.RWMutex
}

func NewProcessInfo() *ProcessInfo {
	pi := ProcessInfo{map[string]string{}, sync.RWMutex{}}
	return &pi
}

func buildStatus(m *ProcessInfo, statusHeader string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.labels[StatusHeader] = statusHeader
	m.labels[EnvVarNodeName] = os.Getenv(EnvVarNodeName)
	m.labels[EnvVarPodName] = os.Getenv(EnvVarPodName)
	m.labels[EnvVarPodNamespace] = os.Getenv(EnvVarPodNamespace)
	m.labels[EnvVarPodIP] = os.Getenv(EnvVarPodIP)
	m.labels[EnvVarPodServiceAccount] = os.Getenv(EnvVarPodServiceAccount)
	m.labels[ServerStatus] = "Initializing"
}

func (m ProcessInfo) SetHealth(newStatus string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.labels[ServerStatus] = newStatus
}

func (m ProcessInfo) GetStatusHeader() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[StatusHeader]
}

func (m ProcessInfo) GetNodeName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[EnvVarNodeName]
}

func (m ProcessInfo) GetPodName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[EnvVarPodName]
}

func (m ProcessInfo) GetPodNamespace() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[EnvVarPodNamespace]
}

func (m ProcessInfo) GetPodIP() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[EnvVarPodIP]
}

func (m ProcessInfo) GetServerStatus() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[ServerStatus]
}

func main() {
	flag.Parse()
	fmt.Printf("Istio Service Mesh Sync Agent.\n")
	flag.VisitAll(func(f *flag.Flag) {
		fmt.Printf("--%-15.15s:%s\n", f.Name, f.Value.String())
	})

	pi := NewProcessInfo()
	buildStatus(pi, ServerStatusHeader)
	si := StatuszInfo{pi, nil, nil}

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
			fmt.Printf("Error in healthz.html\n%s", err.Error())
		}
	})
	http.HandleFunc("/statusz.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(*templateDir, "/statusz.css"))
	})

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

	pl := mesh.NewPodList(*nsIgnoreRegex)
	sl := mesh.NewServiceList(*nsIgnoreRegex, pl)

	agentClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	agent := mesh.NewMeshSyncAgent(agentClient, sl, pi)
	podHandler := controllers.NewPodHandler(pl)

	podClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	podController := controllers.CreateController(podClient, podHandler)

	svcHandler := controllers.NewServiceHandler(sl)
	svcClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	svcController := controllers.CreateController(svcClient, svcHandler)

	mshHandler := controllers.NewMeshHandler(agent)
	mshClient, err := meshv1.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	mshController := controllers.CreateCrdController(mshClient, mshHandler)

	// Now let's start the controllers
	stop := make(chan struct{})
	defer close(stop)

	go podController.Run(1, stop)
	go svcController.Run(1, stop)
	go mshController.Run(1, stop)
	go agent.Run(stop)

	http.HandleFunc("/mesh/v1/endpoints/", func(w http.ResponseWriter, r *http.Request) {
		b := agent.GetExportedEndpoints()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(b)
	})

	go http.ListenAndServe(":"+*httpPort, nil)

	// Wait forever
	select {}
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
