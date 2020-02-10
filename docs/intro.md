# Getting started with CoreRAD

This document describes how to get started with basic configuration and
operation of CoreRAD.

CoreRAD is designed to be highly configurable and extensible, but it also uses
sane defaults as recommended by the IPv6 NDP RFCs and existing software such
as `radvd`.

## Configuration

A complete configuration file for CoreRAD can be found at [`internal/config/default.toml`](https://github.com/mdlayher/corerad/blob/master/internal/config/default.toml).

This configuration file can be written to `corerad.toml` locally by running:

```text
$ corerad -init
$ head -n 1 corerad.toml
# CoreRAD v0.2.1 (BETA) configuration file
```

Although numerous configuration parameters are available, many of them are
unnecessary for typical use. Omitting them will use sane defaults.

Here is an example of a minimal configuration which:

- sends router advertisements from interface `eth0` which indicate this machine
  can be a used as an IPv6 default router
- serves prefix information and allows stateless address autoconfiguration
  (SLAAC) for each /64 prefix on `eth0`
- serves CoreRAD's Prometheus metrics

```toml
# CoreRAD v0.2.1 (BETA) configuration file

[[interfaces]]
name = "eth0"
advertise = true

  [[interfaces.prefix]]
  prefix = "::/64"

[debug]
address = "localhost:9430"
prometheus = true
```

You can verify this configuration using a tool such as `ndp` on a machine that
is on the same network as the router running CoreRAD:

```text
$ go install github.com/mdlayher/ndp/cmd/ndp
go: finding github.com/mdlayher/ndp latest
$ ndp -i eth0 rs
ndp> interface: eth0, link-layer address: 04:d9:f5:7e:1c:47, IPv6 address: fe80::6d9:f5ff:fe7e:1c47
ndp rs> router solicitation:
  - source link-layer address: 04:d9:f5:7e:1c:47

ndp rs> router advertisement from: fe80::20d:b9ff:fe53:eacd:
  - hop limit:        64
  - preference:       Medium
  - router lifetime:  30m0s
  - options:
    - prefix information: 2600:6c4a:787f:d100::/64, flags: [on-link, autonomous], valid: 24h0m0s, preferred: 4h0m0s
    - prefix information: fd9e:1a04:f01d::/64, flags: [on-link, autonomous], valid: 24h0m0s, preferred: 4h0m0s
    - source link-layer address: 00:0d:b9:53:ea:cd
```

## Operation

On Linux machines, CoreRAD requires two capabilities to perform its operations:

- `CAP_NET_RAW`: required; necessary to open and use raw ICMPv6 sockets for
  sending and receiving NDP traffic.
- `CAP_NET_ADMIN`: optional, but necessary for CoreRAD to automatically disable
  kernel IPv6 autoconfiguration on advertising interfaces, so the interface does
  not reconfigure itself based on its own router advertisements.

The server is resilient to network state changes and should recover gracefully
from the majority of errors, so long as the error condition is resolved before
retry attempts run out:

```text
$ sudo setcap cap_net_raw,cap_net_admin+ep /usr/local/bin/corerad
$ corerad -c /etc/corerad/corerad.toml
2020/02/10 12:51:23 CoreRAD v0.2.1 (BETA) starting with configuration file "/etc/corerad/corerad.toml"
2020/02/10 12:51:23 starting HTTP debug listener on "localhost:9430": prometheus: true, pprof: false
2020/02/10 12:51:23 eth0: interface not ready, reinitializing
2020/02/10 12:51:23 eth0: retrying initialization, 39 attempt(s) remaining: interface "eth0" does not exist: link not ready
2020/02/10 12:51:26 eth0: retrying initialization, 38 attempt(s) remaining: listen ip6:ipv6-icmp fe80::3004:2dff:fe54:8e9f%eth0: bind: cannot assign requested address
2020/02/10 12:51:29 eth0: "prefix": ::/64 [on-link, autonomous], preferred: 4h0m0s, valid: 24h0m0s
2020/02/10 12:51:29 eth0: "lla": source link-layer address: 32:04:2d:54:8e:9f
2020/02/10 12:51:29 eth0: initialized, advertising from fe80::3004:2dff:fe54:8e9f
```

It is recommended to run CoreRAD on system startup. Here is an example systemd
unit file for CoreRAD:

```ini
[Unit]
After=network.target
Description=CoreRAD IPv6 NDP RA daemon

[Service]
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW
DynamicUser=true
ExecStart=/usr/local/bin/corerad -c=/etc/corerad/corerad.toml
Restart=on-failure
```

If any errors occur during operation, they will be noted in both CoreRAD's logs
and Prometheus metrics.

In addition, the server will monitor for router advertisements sent from other
routers on the same LAN, and will alert (via logging and Prometheus) on any
inconsistencies between its own advertisements and the received advertisements.
