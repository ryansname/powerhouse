## Configuration
Reads first `/etc/voltage-repeater.ini`, then `./voltage-repeater.ini` for a config file that looks like the following.

```
[mqtt]
username = Fill this in
password = put your password here
host = homeassistant.lan

[batterysense]
mac = <mac addr of device>
key = <key from victron app>

[smartsolar]
mac = <mac addr of device>
key = <key from victron app>
```
  
