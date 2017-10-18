package main

import (
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
)

var docRoot = flag.String("doc_root", "data/docroot", "Root for doc serving path")
var httpPort = flag.String("http_port", "8080", "Port for serving http traffic")
var cluster = flag.String ("cluster", os.Getenv("ISTIO_CLUSTER"), "Istio cluster where this process runs")

func buildStatus(m *map[string]string) {
	if h, err := os.Hostname(); err == nil {
		(*m)["hostname"] = h
	} else {
		(*m)["hostname"] = "Unknown!"
	}
	(*m)["cluster"] = *cluster
}

func main() {
	flag.Parse()
	fmt.Printf("Hybrid Demo server.\n")
	flag.VisitAll(func(f *flag.Flag) {
		fmt.Printf("--%-15.15s:%s\n", f.Name, f.Value.String())
	})

	// Handle all templates
	pattern := filepath.Join(*docRoot, "*.html")
	tmpl := template.Must(template.ParseGlob(pattern))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			t := r.FormValue("targetserver")
			var ctx map[string]string = make(map[string]string)
			if len(t) > 0 {
				if resp, err := http.Get("http://" + t + "/statusz.html"); err == nil {
					defer resp.Body.Close()
					if body, err := ioutil.ReadAll(resp.Body); err == nil {
						ctx["targetresp"] = string(body)
					} else {
						ctx["targetresp"] = err.Error()
					}
				} else {
					ctx["targetresp"] = err.Error()
				}
			}
			buildStatus(&ctx)
			ctx["reqhost"] = r.Header.Get(http.CanonicalHeaderKey("host"))
			ctx["remoteaddr"] = r.RemoteAddr
			ctx["xforwardedfor"] = r.Header.Get(http.CanonicalHeaderKey("X-Forwarded-For"))
			tmpl.ExecuteTemplate(w, "index.html", &ctx)
	})
	http.HandleFunc("/statusz.html", func(w http.ResponseWriter, r *http.Request) {
			var ctx map[string]string = make(map[string]string)
			buildStatus(&ctx)
			tmpl.ExecuteTemplate(w, "statusz.html", &ctx)
	})
	
	http.ListenAndServe(":"+*httpPort, nil)
}