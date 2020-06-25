// Copyright 2019 Matt Layher
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

// Command corerad is an extensible and observable IPv6 Neighbor Discovery
// Protocol router advertisement daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"sync"

	"github.com/mdlayher/corerad/internal/build"
	"github.com/mdlayher/corerad/internal/config"
	"github.com/mdlayher/corerad/internal/corerad"
	"github.com/mdlayher/sdnotify"
)

const cfgFile = "corerad.toml"

func main() {
	var (
		cfgFlag  = flag.String("c", cfgFile, "path to configuration file")
		initFlag = flag.Bool("init", false,
			fmt.Sprintf("write out a default configuration file to %q and exit", cfgFile))
	)

	flag.Usage = func() {
		// Indicate version in usage.
		fmt.Printf("%s\nflags:\n", build.Banner())
		flag.PrintDefaults()
	}

	flag.Parse()

	// Assume we are running under systemd or similar and don't print time/date
	// in the logs.
	ll := log.New(os.Stderr, "", 0)

	if *initFlag {
		err := ioutil.WriteFile(
			cfgFile,
			[]byte(fmt.Sprintf(config.Default, build.Banner())),
			0o644,
		)
		if err != nil {
			ll.Fatalf("failed to write default configuration: %v", err)
		}

		return
	}

	// Enable systemd notifications if running under systemd Type=notify.
	n, err := sdnotify.New()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		ll.Fatalf("failed to open systemd notifier: %v", err)
	}

	msg := fmt.Sprintf("%s starting with configuration file %q", build.Banner(), *cfgFlag)
	ll.Print(msg)
	_ = n.Notify(sdnotify.Statusf(msg))

	f, err := os.Open(*cfgFlag)
	if err != nil {
		ll.Fatalf("failed to open configuration file: %v", err)
	}

	cfg, err := config.Parse(f)
	if err != nil {
		ll.Fatalf("failed to parse %q: %v", f.Name(), err)
	}
	_ = f.Close()

	// Use a context to handle cancelation on signal.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	defer wg.Wait()

	go func() {
		defer wg.Done()

		// Wait for signals (configurable per-platform) and then cancel the
		// context to indicate that the process should shut down.
		sigC := make(chan os.Signal, 1)
		signal.Notify(sigC, signals()...)

		s := <-sigC
		msg := fmt.Sprintf("received %s, shutting down", s)
		ll.Print(msg)
		_ = n.Notify(sdnotify.Statusf(msg), sdnotify.Stopping)
		cancel()

		// Stop handling signals at this point to allow the user to forcefully
		// terminate the binary.
		signal.Stop(sigC)
	}()

	// Start the server's goroutines and run until context cancelation.
	s := corerad.NewServer(ll)
	if err := s.Serve(ctx, n, s.BuildTasks(*cfg)); err != nil {
		ll.Fatalf("failed to run: %v", err)
	}
}
