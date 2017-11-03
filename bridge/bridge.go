package main

import (
	"flag"
	"fmt"
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

var templateDir = flag.String("template_dir", "data/templates", "Root path for HTML templates")
var httpPort = flag.String("http_port", "8080", "Port for serving http traffic")
var nsIgnoreRegex = flag.String("namespace_ignore_list", "kube-system", "Regex of namespaces that need to be ignored by this agent")

func main() {
	flag.Parse()
	fmt.Printf("Istio Service Mesh Sync Agent.\n")
	flag.VisitAll(func(f *flag.Flag) {
		fmt.Printf("--%-15.15s:%s\n", f.Name, f.Value.String())
	})

	mi := mesh.NewMeshInfo()
	mi.BuildStatus(mesh.ServerStatusHeader)

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

	// TODO(gnirodi): Investigate if k8s clients are thread safe and consolidate number of
	// clients if possible
	agentClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	agent := mesh.NewMeshSyncAgent(agentClient, sl, mi)
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

	si := mesh.NewStatuszInfo(mi)
	si.Init(agent, *templateDir)

	// Now let's start the controllers
	stop := make(chan struct{})
	defer close(stop)

	go podController.Run(1, stop)
	go svcController.Run(1, stop)
	go mshController.Run(1, stop)
	go agent.Run(stop)

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
