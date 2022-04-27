// Copyright 2019-2022 Matt Layher
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
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mdlayher/corerad/internal/config"
)

func TestServerBuildTasks(t *testing.T) {
	t.Parallel()

	// Since each Task potentially encapsulates a lot of internal state, we
	// just verify the stringified version of each Task to ensure that the
	// appropriate Tasks were built based on input Config.
	tests := []struct {
		name string
		cfg  config.Config
		ss   []string
	}{
		{
			name: "empty config",
			ss:   []string{"link state watcher"},
		},
		{
			name: "debug HTTP",
			cfg: config.Config{
				Debug: config.Debug{Address: ":9430"},
			},
			ss: []string{
				`debug HTTP server ":9430"`,
				"link state watcher",
			},
		},
		{
			name: "full",
			cfg: config.Config{
				Interfaces: []config.Interface{
					{Name: "eth0", Monitor: true},
					{Name: "eth1", Advertise: true},
					// Not configured.
					{Name: "eth2"},
				},
				Debug: config.Debug{Address: ":9430"},
			},
			ss: []string{
				`monitor "eth0"`,
				`advertiser "eth1"`,
				`debug HTTP server ":9430"`,
				"link state watcher",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(NewContext(nil, nil, nil))

			var ss []string
			for _, task := range srv.BuildTasks(tt.cfg, nil) {
				ss = append(ss, task.String())
			}

			if diff := cmp.Diff(tt.ss, ss); diff != "" {
				t.Fatalf("unexpected task strings (-want +got):\n%s", diff)
			}
		})
	}
}

func TestServerServeBasicTasks(t *testing.T) {
	t.Parallel()

	ll := log.New(os.Stderr, "", 0)

	tests := []struct {
		name  string
		task  Task
		check func(t *testing.T)
	}{
		{
			name: "watcher not exist",
			task: &watcherTask{
				watch: func(_ context.Context) error {
					return os.ErrNotExist
				},
				ll: ll,
			},
		},
		{
			name: "watcher OK",
			task: &watcherTask{
				watch: func(ctx context.Context) error {
					<-ctx.Done()
					return nil
				},
				ll: ll,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timer := time.AfterFunc(5*time.Second, func() {
				panic("test took too long")
			})
			defer timer.Stop()

			// Run the Server until a signal is sent and verify it actually halts.
			sigC := make(chan os.Signal, 1)

			var wg sync.WaitGroup
			wg.Add(1)
			defer func() {
				sigC <- os.Interrupt
				wg.Wait()
			}()

			readyC := make(chan struct{})

			go func() {
				defer wg.Done()
				close(readyC)

				crctx := NewContext(ll, nil, nil)
				if err := NewServer(crctx).Serve(sigC, nil, []Task{tt.task}); err != nil {
					panicf("failed to serve: %v", err)
				}
			}()

			<-readyC
			if tt.check != nil {
				tt.check(t)
			}
		})
	}
}

func Test_serve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		mkCtx func() context.Context
		fn    func() error
		ok    bool
	}{
		{
			name: "context canceled",
			mkCtx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			ok: true,
		},
		{
			name: "context deadline exceeded",
			mkCtx: func() context.Context {
				// Kind of a hack to avoid dropping the cancel.
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				time.AfterFunc(1*time.Second, cancel)
				return ctx
			},
			fn: func() error { return &net.OpError{} },
			ok: true,
		},
		{
			name:  "fatal error",
			mkCtx: context.Background,
			fn:    func() error { return errors.New("fatal") },
		},
		{
			name:  "HTTP shutdown",
			mkCtx: context.Background,
			fn:    func() error { return http.ErrServerClosed },
			ok:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := serve(tt.mkCtx(), nil, 5*time.Millisecond, tt.fn)
			if tt.ok && err != nil {
				t.Fatalf("failed to serve: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("expected an error, but none occurred")
			}
			if err != nil {
				t.Logf("err: %v", err)
			}
		})
	}
}
