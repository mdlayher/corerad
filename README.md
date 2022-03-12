# CoreRAD [![Test](https://github.com/mdlayher/corerad/workflows/Test/badge.svg)](https://github.com/mdlayher/corerad/actions) [![GoDoc](https://godoc.org/github.com/mdlayher/corerad?status.svg)](https://godoc.org/github.com/mdlayher/corerad) [![Go Report Card](https://goreportcard.com/badge/github.com/mdlayher/corerad)](https://goreportcard.com/report/github.com/mdlayher/corerad)

CoreRAD is an extensible and observable IPv6 Neighbor Discovery Protocol router
advertisement daemon.

Apache 2.0 Licensed.

To get started with CoreRAD, [check out corerad.net](https://corerad.net/)!

## Overview

CoreRAD has reached v1.0.0 and is considered stable and ready for production
use on Linux routers. Users are welcome to join us on:

- [`#corerad` on Gophers Slack](https://invite.slack.golangbridge.org/)
- [`#corerad` on Libera.Chat](https://web.libera.chat/)

For more information, you can also check out:

- [CoreRAD a new IPv6 router advertisement
  daemon (Matt Layher, February 2020)](https://mdlayher.com/blog/corerad-a-new-ipv6-router-advertisement-daemon/)
  - Matt's blog introduces CoreRAD and provides comparisons with [the `radvd`
    project](https://github.com/radvd-project/radvd).

CoreRAD is inspired by the [CoreDNS](https://coredns.io/) and
[CoreDHCP](https://coredhcp.io/) projects.

## Dashboard

A sample Grafana + Prometheus dashboard for CoreRAD can be found at [`corerad-grafana.json`](https://github.com/mdlayher/corerad/blob/main/corerad-grafana.json).

![CoreRAD Grafana + Prometheus dashboard](https://raw.githubusercontent.com/mdlayher/corerad/main/website/static/img/grafana.png)
