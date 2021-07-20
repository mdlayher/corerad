---
layout: default
title: Operation
menu:
  main:
    title: "Operation"
    weight: 20
---

CoreRAD is a stateless service that reads its configuration file on startup and
will continue to operate indefinitely from that point on. You can [view the
reference full configuration file
online](https://github.com/mdlayher/corerad/blob/main/internal/config/reference.toml),
but the vast majority of these settings are not required for typical home use.

This guide will provide operational information for running CoreRAD on a Linux
machine.

## Operating modes

CoreRAD has two primary operating modes for a given network interface:

- **Advertise**: used on **downstream (LAN-facing)** network interfaces
  - sends IPv6 router advertisements at regular intervals while also responding
    to router solicitations
  - listens for incoming router advertisements from other routers on the same
    segment, comparing them with its own configuration to alert on any
    inconsistencies detected via logging/Prometheus metrics
- **Monitor**: used on **upstream (WAN-facing)** network interfaces
  - listens for incoming router advertisements from default routers upstream to
    export Prometheus metrics regarding default route expiration and other
    parameters

Assuming `eth0` is a downstream interface and `eth1` is an upstream interface,
both modes can be deployed as follows:

```toml
# Advertise an IPv6 default route and SLAAC-capable prefixes on eth0.
[[interfaces]]
name = "eth0"
advertise = true

  # Advertise an on-link, autonomous prefix for all /64 addresses on eth0.
  [[interfaces.prefix]]
  prefix = "::/64"

# Monitor upstream router advertisements on eth1.
[[interfaces]]
name = "eth1"
monitor = true

# Optional: enable Prometheus metrics.
[debug]
address = "localhost:9430"
prometheus = true
```

Finally, the server will watch for network interface state changes and should
recover gracefully from the majority of errors, so long as the error condition
is resolved before retry attempts run out:

```text
$ corerad -c ./corerad.toml 
CoreRAD v0.3.1 (2021-05-28) starting with configuration file "corerad.toml"
starting HTTP debug listener on "localhost:9430": prometheus: true, pprof: false
eth0: interface not ready, reinitializing
eth0: retrying initialization in 250ms, 49 attempt(s) remaining: interface "eth0" is not up: link not ready
eth0: retrying initialization in 500ms, 48 attempt(s) remaining: interface "eth0" is not up: link not ready
eth0: retrying initialization in 750ms, 47 attempt(s) remaining: interface "eth0" is not up: link not ready
eth0: retrying initialization in 1s, 46 attempt(s) remaining: listen ip6:ipv6-icmp fe80::20d:b9ff:fe53:eacd%eth0: bind: cannot assign requested address
eth0: "prefix": ::/64 [2600:6c4a:787f:d100::/64, fd9e:1a04:f01d::/64] [on-link, autonomous], preferred: 4h0m0s, valid: 24h0m0s
eth0: "lla": source link-layer address: 00:0d:b9:53:ea:cd
eth0: initialized, advertising from fe80::20d:b9ff:fe53:eacd
```

## HTTP debug server

An HTTP debug server can be enabled which serves Prometheus metrics, a limited
HTTP API, and `pprof` data for development. Add the following block to your
configuration to bind the HTTP server, adjusting `address` as needed to allow
Prometheus and/or `pprof` to reach the server.

```toml
# Optional: enable Prometheus metrics.
[debug]
address = "localhost:9430"
prometheus = true
# WARNING: do not expose pprof on a public network!
pprof = false
```

You can verify the server is running with `curl`:

```text
$ curl -i localhost:9430
HTTP/1.1 200 OK
Date: Tue, 22 Jun 2021 11:25:29 GMT
Content-Length: 28
Content-Type: text/plain; charset=utf-8

CoreRAD v0.3.1 (2021-05-28)
```

If enabled, you can also check the Prometheus metrics output:

```text
$ curl -s localhost:9430/metrics | grep corerad_advertiser_prefix_autonomous
# HELP corerad_advertiser_prefix_autonomous Indicates whether or not the Autonomous Address Autoconfiguration (SLAAC) flag is enabled for a given advertised prefix.
# TYPE corerad_advertiser_prefix_autonomous gauge
corerad_advertiser_prefix_autonomous{interface="eth0",prefix="2600:6c4a:787f:d100::/64"} 1
corerad_advertiser_prefix_autonomous{interface="eth0",prefix="fd9e:1a04:f01d::/64"} 1
```

## Linux capabilities

CoreRAD requires two [Linux
capabilities](https://man7.org/linux/man-pages/man7/capabilities.7.html) to
perform its operations:

- `CAP_NET_RAW`: **required**; necessary to open and use raw ICMPv6 sockets for
  sending and receiving NDP traffic.
- `CAP_NET_ADMIN`: **optional**, but necessary for CoreRAD to automatically
  disable kernel IPv6 autoconfiguration on advertising interfaces, so the
  interface does not reconfigure itself based on its own router advertisements.

You can assign these capabilities by using `setcap` (or by running the service
under systemd supervision; see the next section):

```text
$ sudo setcap cap_net_raw,cap_net_admin+ep /usr/local/bin/corerad
```

## systemd unit

It is recommended to run CoreRAD on system startup. Here is an example systemd
unit file for CoreRAD, which also takes advantage of `Type=notify` so systemd
can be notified when CoreRAD is starting up, fully started, or stopping.

```ini
[Unit]
After=network.target
Description=CoreRAD IPv6 NDP RA daemon

[Service]
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW
DynamicUser=true
ExecStart=/usr/local/bin/corerad -c=/etc/corerad/corerad.toml
LimitNOFILE=1048576
LimitNPROC=512
NoNewPrivileges=true
NotifyAccess=main
Restart=on-failure
RestartKillSignal=SIGHUP
Type=notify
```

Note also the use of `RestartKillSignal=SIGHUP`. This allows the service to be
restarted without causing IPv6 clients to drop their default route when the
daemon is restarted via `systemctl restart corerad`. This is useful for quickly
changing the configuration or deprecating prefixes which should no longer be
used.

Here is an example of CoreRAD running under systemd supervision on Matt Layher's
NixOS router. Note the `Status` line, indicating that [systemd readiness
notifications](https://www.freedesktop.org/software/systemd/man/sd_notify.html) are in use.

```text
$ systemctl status corerad
● corerad.service - CoreRAD IPv6 NDP RA daemon
     Loaded: loaded (/nix/store/8zkfcmn39l2i04jd0j44vyqqj75561jl-unit-corerad.service/corerad.service; enabled; vendor preset: enabled)
     Active: active (running) since Tue 2021-06-22 07:20:00 EDT; 6min ago
   Main PID: 4004482 (corerad)
     Status: "server started, all tasks running"
         IP: 31.6K in, 108.9K out
         IO: 44.0K read, 0B written
      Tasks: 10 (limit: 4690)
     Memory: 11.0M
        CPU: 1.422s
     CGroup: /system.slice/corerad.service
             └─4004482 /nix/store/hyfx8za34rjx2061hrkiv3jvqdx6wxs7-corerad-0.3.1/bin/corerad -c=/nix/store/738wciz9f4lxzjz1bx4dwlyafarw95gc-corerad.toml

Jun 22 07:20:00 routnerr-2 corerad[4004482]: lan0: initialized, advertising from fe80::20d:b9ff:fe53:eacd
Jun 22 07:20:00 routnerr-2 corerad[4004482]: tengb0: initialized, advertising from fe80::20d:b9ff:fe53:eacd
Jun 22 07:20:00 routnerr-2 corerad[4004482]: iot0: initialized, advertising from fe80::20d:b9ff:fe53:eacd
Jun 22 07:20:00 routnerr-2 corerad[4004482]: lab0: initialized, advertising from fe80::20d:b9ff:fe53:eacd
Jun 22 07:20:00 routnerr-2 systemd[1]: Started CoreRAD IPv6 NDP RA daemon.
```
