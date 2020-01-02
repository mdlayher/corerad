# config

CoreRAD is designed to be highly configurable and extensible.

The initial goal is to support configurations that achieve feature parity with
existing projects such as radvd. More features will be added in the future.

## Example

The default configuration file for CoreRAD can be found [in the `internal/config/default.toml`](https://github.com/mdlayher/corerad/blob/master/internal/config/default.toml) configuration file.

Although numerous configuration parameters are available, many of them are
unnecessary for typical use.

Here is an example of a minimal configuration which:

- sends router advertisements from interface `eth0` which indicate this machine
  can be a used as an IPv6 default router
- serves prefix information and allows stateless address autoconfiguration
  (SLAAC) for each /64 prefix on `eth0`
- serves CoreRAD's Prometheus metrics

```toml
# CoreRAD vALPHA configuration file

[[interfaces]]
name = "eth0"
send_advertisements = true
default_lifetime = "auto"

  [[interfaces.plugins]]
  name = "prefix"
  prefix = "::/64"

[debug]
address = "localhost:9430"
prometheus = true
```
