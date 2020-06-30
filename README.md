# CoreRAD [![Linux Test](https://github.com/mdlayher/corerad/workflows/Linux%20Test/badge.svg)](https://github.com/mdlayher/corerad/actions) [![GoDoc](https://godoc.org/github.com/mdlayher/corerad?status.svg)](https://godoc.org/github.com/mdlayher/corerad) [![Go Report Card](https://goreportcard.com/badge/github.com/mdlayher/corerad)](https://goreportcard.com/report/github.com/mdlayher/corerad)

CoreRAD is an extensible and observable IPv6 Neighbor Discovery Protocol router
advertisement daemon.

Apache 2.0 Licensed.

## Overview

CoreRAD is currently a **beta** project, and will remain as such for the
entirety of the v0.2.x tag series. CoreRAD is currently deployed and running on
several home LAN networks. Early adopters are welcome to join us in `#corerad`
on [Gophers Slack](https://invite.slack.golangbridge.org) or
[the Freenode IRC network](https://webchat.freenode.net/).

CoreRAD is inspired by the [CoreDNS](https://coredns.io/) and
[CoreDHCP](https://coredhcp.io/) projects.

To learn more about CoreRAD, check out [my introductory blog post](https://mdlayher.com/blog/corerad-a-new-ipv6-router-advertisement-daemon/).

## Dashboard

A sample Grafana + Prometheus dashboard for CoreRAD can be found at [`corerad-grafana.json`](https://github.com/mdlayher/corerad/blob/master/corerad-grafana.json).

![CoreRAD Grafana + Prometheus dashboard](https://raw.githubusercontent.com/mdlayher/corerad/master/website/static/img/grafana.png)
