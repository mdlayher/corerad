# CHANGELOG

## v0.3.4
September 3, 2021

Special thanks to [@markpash](https://github.com/markpash) for his help testing
various features in this release.

- CoreRAD will now operate on interfaces which do not have a MAC address, such
  as PPPoE interfaces. Monitor mode works on these interfaces, but advertise
  mode may still have subtle bugs. Feedback is welcome.
- CoreRAD will now log errors when an interface is configured to advertise an
  IPv6 default route (`interfaces.default_lifetime` is non-zero) but is
  incapable of doing so due to IPv6 forwarding being disabled on the interface.
  This will notify the user that their configuration is invalid and they should
  either enable IPv6 forwarding or remove the `interfaces.default_lifetime`
  configuration to resolve the problem.
- IPv6 addresses which are flagged as stable (manage temporary addresses, stable
  privacy, or EUI-64 address format) on Linux are now preferred for automatic
  prefix and RDNSS advertising.
- IPv6 addresses which are flagged as non-stable (deprecated, temporary, or
  tentative) on Linux will no longer be considered candidates for automatic
  prefix and RDNSS advertising.
- The `interfaces.rdnss.servers` and `interfaces.dnssl.domain_names` options now
  verify that all string array arguments are unique.

## v0.3.3
July 20, 2021

- CoreRAD now outputs a **minimal** configuration file when the `-init` flag is
  passed. The user must still adjust interface names for their router, but most
  users won't need to tweak the vast majority of router advertisement settings.
  A [full reference configuration
  file](https://github.com/mdlayher/corerad/blob/main/internal/config/reference.toml)
  remains available online.
- Groups of interfaces can now be configured identically by specifying the
  `interfaces.names` option rather than `interfaces.name`. Note that these
  options are mutually exclusive. The following configurations are equivalent:

Before:
```toml
[[interfaces]]
name = "vlan0"
advertise = true

[[interfaces]]
name = "vlan1"
advertise = true
```

After:
```toml
[[interfaces]]
names = ["vlan0", "vlan1"]
advertise = true
```

- The `interfaces.prefix.prefix` and `interfaces.rdnss.servers` options will now
  apply their most common default settings when these options are unset. The
  following configurations are equivalent:

Before:
```toml
[[interfaces]]
name = "eth0"
advertise = true
  [[interfaces.prefix]]
  prefix = "::/64"
  [[interfaces.rdnss]]
  servers = ["::"]
```

After:
```toml
[[interfaces]]
name = "eth0"
advertise = true
  [[interfaces.prefix]]
  [[interfaces.rdnss]]
```

- The `interfaces.rdnss.servers` option's wildcard value of `::` can be used to
  advertise a DNS server from the configured interfaces, in addition to zero or
  more statically defined DNS servers.

```toml
[[interfaces]]
name = "eth0"
advertise = true
  [[interfaces.rdnss]]
  servers = ["::", "2001:db8::1"]
```

## v0.3.2
June 25, 2021

- RDNSS now supports a `::` wildcard syntax which will choose a suitable DNS
  server address from the same interface, preferring IPv6 Unique Local
  Addresses, then Global Unicast Addresses, then Link-Local Addresses.
- New Captive Portal option for router advertisements, per [RFC 8910, section
  2.3](https://www.rfc-editor.org/rfc/rfc8910.html#name-the-captive-portal-ipv6-ra-).

## v0.3.1
May 28, 2021

- Prefixes advertised automatically via `::/64` are pulled from each configured
  interface and logged on startup
- Additional checks to prevent the use of IPv6-mapped-IPv4 addresses

## v0.3.0
January 3, 2021

- It's stable and should work great for home use cases! Go forth and use it!
