package lb

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// Proxy is a reverse proxy that routes based on the route table
type Proxy struct {
	routeTable *RouteTable
}

// NewProxy creates a new proxy
func NewProxy(routeTable *RouteTable) *Proxy {
	return &Proxy{
		routeTable: routeTable,
	}
}

// ServeHTTP handles incoming requests
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := p.routeTable.Match(r.Host)
	if route == nil {
		http.Error(w, "no route for host", http.StatusBadGateway)
		return
	}

	backend := route.GetHealthyBackend()
	if backend == nil {
		http.Error(w, "no healthy backend", http.StatusServiceUnavailable)
		return
	}

	target, err := url.Parse("http://" + backend.Address)
	if err != nil {
		http.Error(w, "invalid backend", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("Proxy error for %s -> %s: %v", r.Host, backend.Address, err)
		http.Error(w, "backend error", http.StatusBadGateway)
	}

	log.Printf("%s %s -> %s", r.Method, r.Host+r.URL.Path, backend.Address)
	proxy.ServeHTTP(w, r)
}
