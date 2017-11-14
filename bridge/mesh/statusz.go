package mesh

import (
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"path/filepath"
)

type StatuszInfo struct {
	MeshInfo *MeshInfo
	// Query parms from user request passed to templates
	Refresh               *string
	TargetUrl             *string
	TargetHealthzResponse *string
	QueryParms            EndpointDisplayInfo
	QueryResult           []*EndpointDisplayInfo
}

func NewStatuszInfo(mi *MeshInfo) *StatuszInfo {
	csi := StatuszInfo{mi, nil, nil, nil, EndpointDisplayInfo{}, []*EndpointDisplayInfo{}}
	return &csi
}

func (si *StatuszInfo) clone() *StatuszInfo {
	csi := NewStatuszInfo(si.MeshInfo)
	return csi
}

func (si *StatuszInfo) Init(agent *MeshSyncAgent, templateDir string) {
	// Handle all templates
	pattern := filepath.Join(templateDir, "*")
	tmpl := template.Must(template.ParseGlob(pattern))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		csi := si.clone()

		GetParmValue(r, "refresh", &csi.Refresh)
		hasTarget := GetParmValue(r, "targetserver", &csi.TargetUrl)
		hasZone := GetParmValue(r, "zone", &csi.QueryParms.Zone)
		hasService := GetParmValue(r, "svc", &csi.QueryParms.Service)
		hasNamespace := GetParmValue(r, "ns", &csi.QueryParms.Namespace)
		GetParmValue(r, "lbls", &csi.QueryParms.CsvLabels)
		GetParmValue(r, "all", &csi.QueryParms.AllLabels)
		switch {
		case hasTarget:
			// Get health from the specified target
			var targetResp string
			if resp, err := http.Get("http://" + *(csi.TargetUrl) + "/healthz.html"); err == nil {
				defer resp.Body.Close()
				if body, err := ioutil.ReadAll(resp.Body); err == nil {
					targetResp = string(body)
					csi.TargetHealthzResponse = &targetResp
				} else {
					targetResp = err.Error()
					csi.TargetHealthzResponse = &targetResp
				}
			} else {
				targetResp = err.Error()
				csi.TargetHealthzResponse = &targetResp
			}
			break
		case hasZone || hasService || hasNamespace:
			csi.QueryResult = agent.ExecuteEndpointQuery(&csi.QueryParms)
			break
		}
		err := tmpl.ExecuteTemplate(w, "statusz.html", csi)
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
	http.HandleFunc("/favicon.png", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(templateDir, "/favicon.png"))
	})
}

func GetParmValue(r *http.Request, queryParmName string, statusPtr **string) bool {
	queryParm := r.FormValue(queryParmName)
	hasParm := queryParm != ""
	if hasParm {
		*statusPtr = &queryParm
	}
	return hasParm
}
