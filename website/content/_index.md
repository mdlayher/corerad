---
layout: default
title: Home
menu:
  main:
    title: "Home"
    weight: 1
---

**CoreRAD** is an extensible and observable [IPv6 Neighbor Discovery
Protocol](https://en.wikipedia.org/wiki/Neighbor_Discovery_Protocol) router
advertisement daemon, released under the Apache 2.0 License.

[![CoreRAD Grafana + Prometheus dashboard](/corerad-grafana.png)](/corerad-grafana.png)

## Features

CoreRAD is a modern IPv6 NDP router advertisement daemon, written in Go.

Some of the project's key features include:

- support for [Prometheus metrics](https://prometheus.io/) for dashboards and
  alerting
- monitoring of upstream (WAN-facing) router advertisements to determine if and when an IPv6
  default route will expire
- monitoring of downstream (LAN-facing) router advertisements to ensure they do
  not conflict with CoreRAD's own configuration
- an HTTP API for troubleshooting and debugging
- flexible configuration which can be tailored for each advertising network
  interface

Future goals include:

- dynamic router advertisement configuration via HTTP and/or gRPC APIs
- online configuration reload when `SIGHUP` is received
- expanded HTTP API capabilities
- better support for *BSD and other platforms (these mostly work today, with
  some caveats)

## Resources

CoreRAD is currently deployed and running on several home LAN networks. Early
adopters are welcome to join us on:

- [`#corerad` on Freenode](https://webchat.freenode.net/)
- [`#corerad` on Gophers Slack](https://invite.slack.golangbridge.org/)

For more information, you can also check out:

- [CoreRAD a new IPv6 router advertisement
  daemon (Matt Layher, February 2020)](https://mdlayher.com/blog/corerad-a-new-ipv6-router-advertisement-daemon/)
  - Matt's blog introduces CoreRAD and provides comparisons with [the `radvd`
    project](https://github.com/reubenhwk/radvd).
