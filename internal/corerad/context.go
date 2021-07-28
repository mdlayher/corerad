// Copyright 2020-2021 Matt Layher
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

package corerad

import (
	"errors"
	"io/ioutil"
	"log"

	"github.com/mdlayher/corerad/internal/system"
	"github.com/mdlayher/metricslite"
)

// A Context carries application context and telemetry throughout the Server
// and its Tasks.
type Context struct {
	ll    *log.Logger
	mm    *Metrics
	state system.State
}

// NewContext produces a Context for use with a Server. If any of the inputs
// are nil, a no-op implementation will be used.
func NewContext(ll *log.Logger, mm *Metrics, state system.State) *Context {
	if ll == nil {
		ll = log.New(ioutil.Discard, "", 0)
	}

	if mm == nil {
		mm = NewMetrics(metricslite.Discard(), nil, nil)
	}

	if state == nil {
		state = system.TestState{
			Error: errors.New("no context state type configured"),
		}
	}

	return &Context{
		ll:    ll,
		mm:    mm,
		state: state,
	}
}
