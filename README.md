# Dynamic DNS Updater

Pulls IPs from interfaces, joins them with partial IPs specified in the config file, and sends a RFC 2136-compliant update to a specified server.

# Config File

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

As in BIND, `@` is shorthand for the root of the zone.

While the host entries look like CIDR notation, they truly specify the prefix of the interface IP to merge with.
That is to say, `0.0.0.9/24` + `1.2.3.4` yields `1.2.3.9`.

# Example Scenario

Imagine the following situation:

```
eth0: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet 198.100.123.139  netmask 255.255.255.0  broadcast 10.128.3.255

eth1: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 2001:470:1f0e:83f::2  prefixlen 64  scopeid 0x0<global>
```

```
$ dnsup -4 eth0 -6 eth1 -c example.yml
```

The following updates will be made:

* fully.qualified.update.zone. -> `198.100.123.139`
* host1.fully.qualified.update.zone. -> `2001:0470:1f0e:083f:1234:1234:1234:1234`
* host2.fully.qualified.update.zone. -> `2001:0470:1f0e:083f:def0:0dea:dbee:f000`
* host3.fully.qualified.update.zone. -> `2001:0470:1f0e:083f:abcd:ef01:0234:5678`
