# CHANGELOG

## v0.3.2

- RDNSS now supports a `::` wildcard syntax which will choose a suitable DNS
  server address from the same interface, preferring IPv6 Unique Local
  Addresses, then Global Unicast Addresses, then Link-Local Addresses.
- New Captive Portal option for router advertisements, per [RFC 8910, section
  2.3](https://www.rfc-editor.org/rfc/rfc8910.html#name-the-captive-portal-ipv6-ra-).

## v0.3.1

- Prefixes advertised automatically via `::/64` are pulled from each configured
  interface and logged on startup
- Additional checks to prevent the use of IPv6-mapped-IPv4 addresses

## v0.3.0

- It's stable and should work great for home use cases! Go forth and use it!
