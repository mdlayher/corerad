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
