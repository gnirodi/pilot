package main

import (
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	StatusHeader            = "APP_NAME"
)

var docRoot = flag.String("template_dir", "data/templates", "Root for http templates")
var httpPort = flag.String("http_port", "8080", "Port for serving http traffic")

func buildStatus(m *map[string]string) {
	(*m)[StatusHeader] = os.Getenv(StatusHeader)
	(*m)[EnvVarNodeName] = os.Getenv(EnvVarNodeName)
	(*m)[EnvVarPodName] = os.Getenv(EnvVarPodName)
	(*m)[EnvVarPodNamespace] = os.Getenv(EnvVarPodNamespace)
	(*m)[EnvVarPodIP] = os.Getenv(EnvVarPodIP)
	(*m)[EnvVarPodServiceAccount] = os.Getenv(EnvVarPodServiceAccount)
	(*m)[ServerStatus] = "OK"
}

type CachedEndpoints struct {
	target       string
	targetLabels string
	hostPorts    []string
	expires      time.Time
	resolver     *mesh.MeshResolver
	err          error
	mu           sync.RWMutex
}

func NewCachedEndpoints() *CachedEndpoints {
	return &CachedEndpoints{"", "", []string{}, time.Now(), nil, nil, sync.RWMutex{}}
}

func (c *CachedEndpoints) Init(kubeconfig *string) {
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		// use the current context in kubeconfig (dev / raw VM)
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err.Error())
		}
	}

	resolverClientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	c.resolver = mesh.NewMeshResolver(resolverClientset)
}

func (c *CachedEndpoints) GetEndpoints(target, targetLabels string) (string, error) {
	labels := map[string]string{}
	nvPairs := strings.Split(targetLabels, ",")
	for _, nvPair := range nvPairs {
		if nvPair != "" {
			lblNameValues := strings.Split(nvPair, "=")
			if len(lblNameValues) >= 2 {
				labels[lblNameValues[0]] = lblNameValues[1]
			}
		}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if target != c.target || targetLabels != c.targetLabels || time.Now().After(c.expires) {
		c.mu.RUnlock()
		defer c.mu.RLock()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.hostPorts, c.err = c.resolver.GetSvcEndpointsBySvcName(target, labels)
		if c.err == nil && len(c.hostPorts) > 0 {
			c.expires = time.Now().Add(time.Second * 3)
			c.target = target
			c.targetLabels = targetLabels
		}
	}
	if c.err != nil || len(c.hostPorts) == 0 {
		return "", c.err
	}
	return c.hostPorts[rand.Intn(len(c.hostPorts))], nil
}

func main() {
	// Start syncing state
	var kubeconfig *string
	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	fmt.Printf("Mesh Demo Test Server.\n")
	flag.VisitAll(func(f *flag.Flag) {
		fmt.Printf("--%-15.15s:%s\n", f.Name, f.Value.String())
	})

	var serverStatus map[string]string = make(map[string]string)
	buildStatus(&serverStatus)

	// Handle all templates
	pattern := filepath.Join(*docRoot, "*.html")
	tmpl := template.Must(template.ParseGlob(pattern))
	http.HandleFunc("/healthz.html", func(w http.ResponseWriter, r *http.Request) {
		err := tmpl.ExecuteTemplate(w, "healthz.html", &serverStatus)
		if err != nil {
			fmt.Printf("Error in statusz.html:\n%s", err.Error())
		}
	})

	cachedResolver := NewCachedEndpoints()
	cachedResolver.Init(kubeconfig)

	http.HandleFunc("/ping.html", func(w http.ResponseWriter, r *http.Request) {
		services := GetParmValue(r, "services")
		isFirstSvc := true
		nextPingTarget := ""
		nextPingTargetLbls := ""
		nextPingSvcList := ""
		svcParms := map[string]string{}
		err := tmpl.ExecuteTemplate(w, "healthz.html", &serverStatus)
		if err != nil {
			fmt.Printf("Error in statusz.html:\n%s", err.Error())
			return
		}
		if services == nil {
			return
		}
		svcList := strings.Split(*services, ",")
		if services != nil {
			for _, svc := range svcList {
				if svc != "" {
					if isFirstSvc {
						isFirstSvc = false
						nextPingTarget = svc
					} else {
						if nextPingSvcList == "" {
							nextPingSvcList = svc
						} else {
							nextPingSvcList = nextPingSvcList + "," + svc
						}
						parms := GetParmValue(r, "serviceParms_"+svc)
						if parms != nil {
							svcParms[svc] = *parms
						}
					}
				}
			}
		}
		if nextPingTarget != "" {
			separator := "\n" + strings.Repeat("-", 20) + "\n"
			w.Write([]byte(separator))
			hostPort, err := cachedResolver.GetEndpoints(nextPingTarget, nextPingTargetLbls)
			if err != nil {
				errMsg := fmt.Sprintf("Error resolving target '%s' for labels '%s'. Error: '%s'", nextPingTarget, nextPingTargetLbls, err.Error())
				w.Write([]byte(errMsg))
				return
			}
			if hostPort == "" {
				errMsg := fmt.Sprintf("Error resolving target '%s' for labels '%s'", nextPingTarget, nextPingTargetLbls)
				w.Write([]byte(errMsg))
				return
			}
			targetUrl := "http://" + hostPort + "/ping.html?services=" + nextPingSvcList
			for s, p := range svcParms {
				targetUrl = targetUrl + "&serviceParms_" + s + "=" + p
			}
			if resp, err := http.Get(targetUrl); err == nil {
				defer resp.Body.Close()
				if body, err := ioutil.ReadAll(resp.Body); err == nil {
					w.Write(body)
				} else {
					w.Write([]byte(err.Error() + "\n"))
				}
			} else {
				w.Write([]byte(err.Error() + "\n"))
			}
		}
	})

	http.ListenAndServe(":"+*httpPort, nil)
}

func GetParmValue(r *http.Request, queryParmName string) *string {
	queryParm := r.FormValue(queryParmName)
	if queryParm != "" {
		return &queryParm
	}
	return nil
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
