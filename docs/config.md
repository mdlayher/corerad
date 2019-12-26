# config

CoreRAD is designed to be highly configurable and extensible.

The initial goal is to support configurations that achieve feature parity with
existing projects such as radvd. More features will be added in the future.

## Example

Here's an example of what a hypothetical CoreRAD configuration could look like.

```toml
# CoreRAD vALPHA configuration file

# Interfaces which may be configured to send router advertisements.
[[interfaces.eth0]]
# AdvSendAdvertisements: indicates whether or not this interface will send
# periodic router advertisements and respond to router solicitations.
#
# Can be used to quickly toggle an individual interface on and off.
send_advertisements = true

# Individual NDP parameters may be tweaked on a per-interface basis. If not
# specified, they will use the default values as defined in the RFC.
max_advertise_interval = "600s"
min_advertise_interval = "auto" # computed based on max

  # Plugins are invoked in order and each is parsed using a unique name.
  [[interfaces.plugins]]
  name = "prefix" # This is a "prefix" plugin.

  # Key/values beyond this point are specific to a given plugin.

  # The "wildcard" prefix syntax is also supported, as with radvd. That means
  # all available /64 prefixes on this interface will be served with the
  # specified configuration in this block.
  prefix = "::/64"
  autonomous = true
  on_link = true

  # Specify a specific prefix on this interface to configure it explicitly with
  # a higher precedence than the wildcard block above. Say for example, you want
  # one prefix on this interface to be advertised for DHCPv6 use only.
  [[interfaces.plugins]]
  name = "prefix"
  prefix = "2001:db8::ffff/64"
  autonomous = true
  managed = true
  other_config = true

  # More static configuration for other NDP options.
  [[interfaces.plugins]]
  name = "rdnss"
  servers = ["fd00::1"]

# Another serving interface with a dynamic configuration.
[[interfaces.eth1]]
send_advertisements = true

  # A hypothetical HTTP plugin which consults an external server by passing it
  # the request to determine what sorts of prefixes, options, etc. to serve.
  [[interfaces.plugins]]
  name = "http"
  address = "https://ipam.example.com/corerad"
  timeout = "5s"

# Enable or disable the debug HTTP server for facilities such as Prometheus
# metrics and pprof support.
#
# Warning: do not expose pprof on an untrusted network!
[debug]
address = "localhost:9430"
prometheus = true
pprof = false
```
