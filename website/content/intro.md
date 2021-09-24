---
layout: default
title: Intro
menu:
  main:
    title: "Intro"
    weight: 10
---

This guide introduces a minimal CoreRAD configuration which can be used to
provide IPv6-enabled hosts with a default route and IPv6 addresses via
[Stateless Address Autoconfiguration
(SLAAC)](https://en.wikipedia.org/wiki/IPv6#Stateless_address_autoconfiguration_(SLAAC)).

This guide will assumes a Linux-based router with the following configuration:

- a LAN-facing network interface named `eth0`
- IPv6 addresses from one or more `/64` subnets configured on `eth0`, via static
  assignment, DHCPv6 prefix delegation, or another mechanism
- IPv6 forwarding enabled for `eth0`

```text
$ ip -6 addr show dev eth0
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc mq state UP group default qlen 1000
    inet6 fd9e:1a04:f01d::1/64 scope global
       valid_lft forever preferred_lft forever
    inet6 2600:6c4a:787f:d100::1/64 scope global dynamic noprefixroute
       valid_lft 537884sec preferred_lft 537884sec
    inet6 fe80::20d:b9ff:fe53:eacd/64 scope link
       valid_lft forever preferred_lft forever
```
```text
$ cat /proc/sys/net/ipv6/conf/eth0/forwarding
0
$ echo 1 | sudo tee /proc/sys/net/ipv6/conf/eth0/forwarding
1
```

Create a minimal `corerad.toml` configuration file with the following text:

```toml
# Advertise an IPv6 default route and SLAAC-capable prefixes on eth0.
[[interfaces]]
name = "eth0"
advertise = true

  # Advertise an on-link, autonomous prefix for all /64 addresses on eth0.
  [[interfaces.prefix]]

# Optional: enable Prometheus metrics.
[debug]
address = "localhost:9430"
prometheus = true
```

As of July 2021, CoreRAD packages are available for:

- [Alpine Linux](https://pkgs.alpinelinux.org/packages?name=corerad&branch=edge)
- [NixOS](https://search.nixos.org/packages?query=corerad)

For other Linux distributions or operating systems, download and build [the
latest CoreRAD release from
source](https://github.com/mdlayher/corerad/releases). A Go 1.17+ compiler is
required.

```text
$ go build ./cmd/corerad/
$ ./corerad -h
CoreRAD v0.3.3 (2021-07-20)
flags:
  -c string
        path to configuration file (default "corerad.toml")
  -init
        write out a minimal configuration file to "corerad.toml" and exit
```

Ensure the CoreRAD binary has the [Linux
capabilities](https://man7.org/linux/man-pages/man7/capabilities.7.html)
`CAP_NET_ADMIN` and `CAP_NET_RAW` to appropriately manage network interfaces and
handle raw ICMPv6 NDP traffic:

```text
$ sudo setcap cap_net_raw,cap_net_admin+ep ./corerad
```

Finally, start CoreRAD with the configuration file:

```text
$ ./corerad -c ./corerad.toml
CoreRAD v0.3.3 (2021-07-20) starting with configuration file "corerad.toml"
starting HTTP debug listener on "localhost:9430": prometheus: true, pprof: false
eth0: "prefix": ::/64 [2600:6c4a:787f:d100::/64, fd9e:1a04:f01d::/64] [on-link, autonomous], preferred: 4h0m0s, valid: 24h0m0s
eth0: "lla": source link-layer address: 00:0d:b9:53:ea:cd
eth0: initialized, advertising from fe80::20d:b9ff:fe53:eacd
```

Client machines on the router's LAN should now have an IPv6 default route and
one or more IPv6 addresses per advertised prefix, generated via SLAAC:

```text
client $ ip -6 route show dev eth0
2600:6c4a:787f:d100::/64 proto ra metric 101 pref medium
fd9e:1a04:f01d::/64 proto ra metric 101 pref medium
fe80::/64 proto kernel metric 101 pref medium
default via fe80::20d:b9ff:fe53:eacd proto ra metric 20101 pref medium
```
```text
client $ ip -6 addr show dev eth0
2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc mq state UP group default qlen 1000
    inet6 2600:6c4a:787f:d100:e446:69ee:4970:114e/64 scope global temporary dynamic
       valid_lft 86011sec preferred_lft 14011sec
    inet6 2600:6c4a:787f:d100:6d9:f5ff:fe7e:1c47/64 scope global dynamic mngtmpaddr noprefixroute
       valid_lft 86011sec preferred_lft 14011sec
    inet6 fd9e:1a04:f01d:0:e446:69ee:4970:114e/64 scope global temporary dynamic
       valid_lft 86011sec preferred_lft 14011sec
    inet6 fd9e:1a04:f01d:0:6d9:f5ff:fe7e:1c47/64 scope global dynamic mngtmpaddr noprefixroute
       valid_lft 86011sec preferred_lft 14011sec
    inet6 fe80::6d9:f5ff:fe7e:1c47/64 scope link noprefixroute
       valid_lft forever preferred_lft forever
```

Optionally, check out CoreRAD's Prometheus metrics output:

```text
$ curl -s localhost:9430/metrics | grep corerad_advertiser_prefix_autonomous
# HELP corerad_advertiser_prefix_autonomous Indicates whether or not the Autonomous Address Autoconfiguration (SLAAC) flag is enabled for a given advertised prefix.
# TYPE corerad_advertiser_prefix_autonomous gauge
corerad_advertiser_prefix_autonomous{interface="eth0",prefix="2600:6c4a:787f:d100::/64"} 1
corerad_advertiser_prefix_autonomous{interface="eth0",prefix="fd9e:1a04:f01d::/64"} 1
```
