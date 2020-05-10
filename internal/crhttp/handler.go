// Copyright 2020 Matt Layher
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package crhttp

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/pprof"

	"github.com/mdlayher/corerad/internal/build"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Common HTTP content types.
const (
	contentText = "text/plain; charset=utf-8"
	contentJSON = "application/json; charset=utf-8"
)

// A Handler provides the HTTP debug API handler for CoreRAD.
type Handler struct {
	h      http.Handler
	ll     *log.Logger
	ifaces []config.Interface
	state  system.State
}

// NewHandler creates a Handler with the specified configuration.
func NewHandler(
	ll *log.Logger,
	state system.State,
	ifaces []config.Interface,
	usePrometheus, usePProf bool,
	reg *prometheus.Registry,
) *Handler {
	mux := http.NewServeMux()

	h := &Handler{
		h: mux,

		// TODO(mdlayher): use to build out other API handlers.
		ll:     ll,
		ifaces: ifaces,
		state:  state,
	}

	// Plumb in debugging API handlers.
	mux.HandleFunc("/api/interfaces", h.interfaces)

	// Optionally enable Prometheus and pprof support.
	if usePrometheus {
		mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	}

	if usePProf {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Matching on "/" would produce an overly broad rule, so check manually
	// here and indicate that this is the CoreRAD service.
	if r.URL.Path == "/" {
		_, _ = io.WriteString(w, build.Banner()+"\n")
		return
	}

	h.h.ServeHTTP(w, r)
}

// interfaces returns a JSON representation of the advertising state of each
// configured interface.
func (h *Handler) interfaces(w http.ResponseWriter, r *http.Request) {
	body := interfacesBody{
		Interfaces: make([]interfaceBody, 0, len(h.ifaces)),
	}

	for i, iface := range h.ifaces {
		body.Interfaces = append(body.Interfaces, interfaceBody{
			Interface:   iface.Name,
			Advertising: iface.Advertise,
		})

		if !iface.Advertise {
			// For interfaces which are not advertising, only report basic
			// information with null RA output.
			continue
		}

		forwarding, err := h.state.IPv6Forwarding(iface.Name)
		if err != nil {
			h.errorf(w, "failed to check interface %q forwarding state: %v", iface.Name, err)
			return
		}

		ra, err := iface.RouterAdvertisement(forwarding)
		if err != nil {
			h.errorf(w, "failed to generate router advertisements: %v", err)
			return
		}

		body.Interfaces[i].Advertisement = packRA(ra)
	}

	// TODO: factor out JSON serving middleware.
	w.Header().Set("Content-Type", contentJSON)

	_ = json.NewEncoder(w).Encode(body)
}

func (h *Handler) errorf(w http.ResponseWriter, format string, v ...interface{}) {
	err := fmt.Errorf(format, v...)
	h.ll.Printf("HTTP server error: %v", err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
}
