# Dynamic DNS Updater

Pulls IPs from interfaces, joins them with partial IPs specified in the config file, and sends a RFC 2136-compliant update to a specified server.

# Config file

```
server: "ipaddr:53"
zone: "fully.qualified.update.zone."
key: "update.key."
secrets:
        "update.key.": "update.secret."
hosts:
        "host1": "::1234:1234:1234:1234/64"
        "host2": "::def0:dea:dbee:f000/64"
        "host3": "::abcd:ef01:234:5678/64"
        "@": "0.0.0.0/32"
ttl: 300
```

In the above example, `"@": "0.0.0.0/32"` updates `fully.qualified.update.zone.` to match the IPv4 address pulled from the v4 interface, and hosts 1-3 to the first 64 bits of the IPv6 address from the v6 interface, plus the last 64 bits specified in the config file.
