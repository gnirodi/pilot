package mesh

import (
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"path/filepath"

	"k8s.io/client-go/kubernetes"
)

type StatuszInfo struct {
	MeshInfo *MeshInfo
	// Query parms from user request passed to templates
	Refresh               *string
	TargetUrl             *string
	TargetHealthzResponse *string
	Service               *string
	Zone                  *string
	queryClientset        *kubernetes.Clientset
}

func NewStatuszInfo(mi *MeshInfo) *StatuszInfo {
	csi := StatuszInfo{mi, nil, nil, nil, nil, nil, nil}
	return &csi
}

func (si *StatuszInfo) clone() *StatuszInfo {
	csi := NewStatuszInfo(si.MeshInfo)
	return csi
}

func (si *StatuszInfo) Init(queryClientset *kubernetes.Clientset, templateDir string) {
	// Forward declaration to be used with statusz queries. Populated down below
	si.queryClientset = queryClientset

	// Handle all templates
	pattern := filepath.Join(templateDir, "*")
	tmpl := template.Must(template.ParseGlob(pattern))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		csi := si.clone()
		refresh := r.FormValue("refresh")
		if refresh == "1" {
			csi.Refresh = &refresh
		} else {
			csi.Refresh = nil
		}
		target := r.FormValue("targetserver")
		zone := r.FormValue("zone")
		service := r.FormValue("service")
		switch {
		case target != "":
			// Get health from the specified target
			var targetResp string
			if resp, err := http.Get("http://" + target + "/healthz.html"); err == nil {
				defer resp.Body.Close()
				if body, err := ioutil.ReadAll(resp.Body); err == nil {
					targetResp = string(body)
					csi.TargetUrl = &target
					csi.TargetHealthzResponse = &targetResp
				} else {
					targetResp = err.Error()
					csi.TargetUrl = &target
					csi.TargetHealthzResponse = &targetResp
				}
			} else {
				targetResp = err.Error()
				csi.TargetHealthzResponse = &targetResp
			}
			break
		case zone != "" || service != "":
			QueryEndpoints(queryClientset, csi)
			break
		default:
			csi.TargetUrl = nil
			csi.TargetHealthzResponse = nil
			break
		}
		err := tmpl.ExecuteTemplate(w, "statusz.html", &csi)
		if err != nil {
			fmt.Printf("Error in statusz.html:\n%s", err.Error())
		}
	})
	http.HandleFunc("/healthz.html", func(w http.ResponseWriter, r *http.Request) {
		err := tmpl.ExecuteTemplate(w, "healthz.html", si.MeshInfo)
		if err != nil {
			fmt.Printf("Error in healthz.html\n%s", err.Error())
		}
	})
	http.HandleFunc("/statusz.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(templateDir, "/statusz.css"))
	})
	http.HandleFunc("/logo.png", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(templateDir, "/logo.png"))
	})
}
