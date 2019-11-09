package main

import (
	"fmt"
	"log"
	"net/http"
	"runtime"
	"text/template"

	"github.com/heistp/cgmon/analyzer"
)

type rootHandler struct {
	tmpl *template.Template
	app  *App
}

func newRootHandler(a *App) *rootHandler {
	thtml := `
<html>
<head>
<title>cgmon {{.Version}}</title>
</head>
<h2>cgmon version {{.Version}}</h2>

<pre>
{{.Metrics}}
</pre>

<a href="/flow-duration-histogram">Show Flow Duration Histogram</a>

<div style="margin-top: 1em">
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

	return &rootHandler{tmpl, a}
}

func (h *rootHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := r.URL.Query()["gc"]; ok {
		runtime.GC()
	}

	d := httpServerData{
		VERSION,
		h.app.DumpMetrics(),
	}

	if err := h.tmpl.Execute(w, d); err != nil {
		log.Printf("http server error executing template (%s)", err)
	}
}

type flowDurationHistogramHandler struct {
	analyzer *analyzer.Analyzer
}

func (h *flowDurationHistogramHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flh, oorp := h.analyzer.FlowDurations.String(80)
	fmt.Fprintf(w, "Flow duration histogram (%.1f%% out of range):\n", oorp)
	fmt.Fprintf(w, flh)
	fmt.Fprintf(w, "\n")
}

type httpServerData struct {
	Version string
	Metrics string
}
