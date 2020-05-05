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
	"io"
	"net/http"
	"net/http/pprof"

	"github.com/mdlayher/corerad/internal/build"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/system"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// A Handler provides the HTTP debug API handler for CoreRAD.
type Handler struct {
	h      http.Handler
	ifaces []config.Interface
	state  system.State
}

// NewHandler creates a Handler with the specified configuration.
func NewHandler(
	state system.State,
	ifaces []config.Interface,
	usePrometheus, usePProf bool,
	reg *prometheus.Registry,
) *Handler {
	mux := http.NewServeMux()

	h := &Handler{
		h: mux,

		// TODO(mdlayher): use to build out other API handlers.
		ifaces: ifaces,
		state:  state,
	}

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
