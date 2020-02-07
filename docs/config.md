# config

CoreRAD is designed to be highly configurable and extensible.

The initial goal is to support configurations that achieve feature parity with
existing projects such as radvd. More features will be added in the future.

## Example

The default configuration file for CoreRAD can be found [in the `internal/config/default.toml`](https://github.com/mdlayher/corerad/blob/master/internal/config/default.toml) configuration file.

This configuration file can be written to `corerad.toml` locally by running:

```text
$ corerad -init
$ head -n 1 corerad.toml
# CoreRAD v0.2.0 (BETA) configuration file
```

Although numerous configuration parameters are available, many of them are
unnecessary for typical use.

Here is an example of a minimal configuration which:

- sends router advertisements from interface `eth0` which indicate this machine
  can be a used as an IPv6 default router
- serves prefix information and allows stateless address autoconfiguration
  (SLAAC) for each /64 prefix on `eth0`
- serves CoreRAD's Prometheus metrics

```toml
# CoreRAD v0.2.0 (BETA) configuration file

[[interfaces]]
name = "eth0"
advertise = true

  [[interfaces.plugins]]
  name = "prefix"
  prefix = "::/64"

[debug]
address = "localhost:9430"
prometheus = true
```

You can verify this configuration using a tool such as `ndp`:

```text
$ go install github.com/mdlayher/ndp/cmd/ndp
go: finding github.com/mdlayher/ndp latest
$ ndp -i lab0 rs
ndp> interface: lab0, link-layer address: 04:d9:f5:7e:1c:47, IPv6 address: fe80::3dc3:e38f:1d2b:c661
ndp rs> router solicitation:
  - source link-layer address: 04:d9:f5:7e:1c:47

ndp rs> router advertisement from: fe80::20d:b9ff:fe53:eacd:
  - hop limit:        64
  - preference:       Medium
  - router lifetime:  30m0s
  - options:
    - prefix information: 2600:6c4a:787f:d102::/64, flags: [OA], valid: 24h0m0s, preferred: 4h0m0s
    - prefix information: fd9e:1a04:f01d:2::/64, flags: [OA], valid: 24h0m0s, preferred: 4h0m0s
    - source link-layer address: 00:0d:b9:53:ea:cd
```
