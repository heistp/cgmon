package main

import (
	"log"
	"net/http"
	"runtime"
	"text/template"

	"github.com/heistp/cgmon/metrics"
)

type rootHandler struct {
	tmpl    *template.Template
	metrics *metrics.Metrics
}

func newRootHandler(m *metrics.Metrics) *rootHandler {
	thtml := `
<html>
<head>
<title>cgmon {{.Version}}</title>
</head>
<h2>cgmon version {{.Version}}</h2>

<pre>
{{.Metrics}}
</pre>

<div>
<form action="/" method="GET" style="float: left; margin-right: 1em">
    <input type="submit" value="Refresh" />
</form>
<form action="/" method="GET">
	<input type="hidden" name="gc" value="1" /> 
    <input type="submit" value="Run GC" />
</form>
</div>

</html>`

	tmpl := template.Must(template.New("thtml").Parse(thtml))

	return &rootHandler{tmpl, m}
}

func (h *rootHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := r.URL.Query()["gc"]; ok {
		runtime.GC()
	}

	d := httpServerData{
		VERSION,
		h.metrics.String(),
	}

	if err := h.tmpl.Execute(w, d); err != nil {
		log.Printf("http server error executing template (%s)", err)
	}
}

type httpServerData struct {
	Version string
	Metrics string
}
