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
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	// Uncomment the following line to load the gcp plugin (only required to authenticate against GKE clusters).
	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	CountRequests           = "COUNT_REQUESTS"
	CountErrors             = "COUNT_ERRORS"
	CountBackendErrors      = "COUNT_BACKEND_ERRORS"
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

type LoadRequest struct {
	target             string
	targetLabels       string
	url                string
	qps                int
	countRequests      int64
	countErrors        int64
	countBackendErrors int64
	cachedResolver     *CachedEndpoints
	requestQueue       chan struct{}
	stop               chan struct{}
	mu                 sync.RWMutex
}

func NewLoadRequest(cachedResolver *CachedEndpoints) *LoadRequest {
	return &LoadRequest{"", "", "", 0, 0, 0, 0, cachedResolver, make(chan struct{}, 5000), make(chan struct{}, 1), sync.RWMutex{}}
}

func (lr *LoadRequest) ReadStats() (int, int64, int64, int64) {
	lr.mu.RLock()
	defer lr.mu.RUnlock()
	return lr.qps, lr.countRequests, lr.countErrors, lr.countBackendErrors
}

func (lr *LoadRequest) ReadTargetParms() (int, string, string, string) {
	lr.mu.RLock()
	defer lr.mu.RUnlock()
	return lr.qps, lr.target, lr.targetLabels, lr.url
}

func (lr *LoadRequest) SetTargetParms(qps int, nextPingTarget, nextPingTargetLbls, nextPingSvcList string) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	lr.qps = qps
	lr.target = nextPingTarget
	lr.targetLabels = nextPingTargetLbls
	lr.url = "/ping.html?services=" + nextPingSvcList
}

func (lr *LoadRequest) UpdateCounters(err error, backendError bool) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	lr.countRequests++
	if err != nil {
		lr.countErrors++
	}
	if backendError {
		lr.countBackendErrors++
	}
}

func (lr *LoadRequest) Run() {
	for {
		<-lr.requestQueue
		qps, target, targetLabels, url := lr.ReadTargetParms()
		if qps > 0 && target != "" && url != "" {
			var err error = nil
			var backendError = false
			hostPort, err := lr.cachedResolver.GetEndpoints(target, targetLabels)
			if err == nil {
				if resp, err := http.Get("http://" + hostPort + url); err == nil {
					defer resp.Body.Close()
					if body, err := ioutil.ReadAll(resp.Body); err == nil {
						respText := string(body)
						if strings.Contains(respText, "Backend Error") {
							backendError = true
						}
					}
				}
			}
			lr.UpdateCounters(err, backendError)
		}
	}
}

func (lr *LoadRequest) RunLoad() {
	qps, target, _, url := lr.ReadTargetParms()
	if qps == 0 || target == "" || url == "" {
		return
	}
	for rq := 0; rq < qps; rq++ {
		var nextReq struct{}
		lr.requestQueue <- nextReq
	}
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

	loadRequest := NewLoadRequest(cachedResolver)
	for i := 0; i < 10; i++ {
		go loadRequest.Run()
	}
	go wait.Until(loadRequest.RunLoad, time.Second, loadRequest.stop)

	http.HandleFunc("/ping.html", func(w http.ResponseWriter, r *http.Request) {
		services := GetParmValue(r, "services")
		isFirstSvc := true
		nextPingTarget := ""
		nextPingTargetLbls := ""
		nextPingSvcList := ""
		svcParms := map[string]string{}

		loadStatus := map[string]string{}
		for k, v := range serverStatus {
			loadStatus[k] = v
		}
		qps, countRequests, countErrors, countBackendErrors := loadRequest.ReadStats()

		if qps > 0 {
			loadStatus[CountRequests] = fmt.Sprintf("%d", countRequests)
			loadStatus[CountErrors] = fmt.Sprintf("%d", countErrors)
			loadStatus[CountBackendErrors] = fmt.Sprintf("%d", countBackendErrors)
		}

		err := tmpl.ExecuteTemplate(w, "healthz.html", &loadStatus)
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
			qpsParm := GetParmValue(r, "qps")
			if qpsParm != nil {
				var qps int
				_, err := fmt.Sscanf(*qpsParm, "%d", &qps)
				if err != nil {
					return
				}
				loadRequest.SetTargetParms(qps, nextPingTarget, nextPingTargetLbls, nextPingSvcList)
				return
			}
			separator := "\n" + strings.Repeat("-", 20) + "\n"
			w.Write([]byte(separator))
			hostPort, err := cachedResolver.GetEndpoints(nextPingTarget, nextPingTargetLbls)
			if err != nil {
				errMsg := fmt.Sprintf("Backend Error resolving target '%s' for labels '%s'. Error: '%s'", nextPingTarget, nextPingTargetLbls, err.Error())
				w.Write([]byte(errMsg))
				return
			}
			if hostPort == "" {
				errMsg := fmt.Sprintf("Backend Error resolving target '%s' for labels '%s'", nextPingTarget, nextPingTargetLbls)
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
					w.Write([]byte("Backend Error: " + err.Error() + "\n"))
				}
			} else {
				w.Write([]byte("Backend Error: " + err.Error() + "\n"))
			}
		}
	})

	http.Handle("/metrics", promhttp.Handler())

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
