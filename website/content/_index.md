---
layout: default
---

**CoreRAD** is an extensible and observable [IPv6 Neighbor Discovery
Protocol](https://en.wikipedia.org/wiki/Neighbor_Discovery_Protocol) router
advertisement daemon, released under the Apache 2.0 License.

CoreRAD has reached v1.x.x and is considered stable and ready for production use
on Linux routers.

You can find the source code at
[`github.com/mdlayher/corerad`](https://github.com/mdlayher/corerad). The latest
release can be found [on the releases
page](https://github.com/mdlayher/corerad/releases) in the repository.

[![CoreRAD Grafana + Prometheus dashboard](/img/grafana.png)](/img/grafana.png)

## Features

CoreRAD is a modern IPv6 NDP router advertisement daemon, written in Go.

Some of the project's key features include:

- support for [Prometheus metrics](https://prometheus.io/) for dashboards and
  alerting
- monitoring of upstream (WAN-facing) router advertisements to determine if and
  when an IPv6 default route will expire
- monitoring of downstream (LAN-facing) router advertisements to ensure they do
  not conflict with CoreRAD's own configuration
- flexible configuration which can be tailored for each advertising network
  interface

Future goals include:

- dynamic router advertisement configuration via HTTP and/or gRPC APIs
- an HTTP API to export network configuration information
- better support for *BSD and other platforms (these mostly work today, with
  some caveats)

## Resources

CoreRAD is deployed and running in production environments. Users are welcome to
join us on:

- [`#corerad` on Gophers Slack](https://invite.slack.golangbridge.org/)
- [`#corerad` on Libera.Chat](https://web.libera.chat/)

For more information, you can also check out:

- [CoreRAD a new IPv6 router advertisement
  daemon (Matt Layher, February 2020)](https://mdlayher.com/blog/corerad-a-new-ipv6-router-advertisement-daemon/)
  - Matt's blog introduces CoreRAD and provides comparisons with [the `radvd`
    project](https://github.com/radvd-project/radvd).
