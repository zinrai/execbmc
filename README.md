# execbmc

A virtual BMC that translates IPMI and Redfish operations into configured commands.

## What it does

execbmc serves IPMI over LAN (RMCP+ and v1.5) and a Redfish subset for one
machine, and executes configured commands for power operations. Boot device
selection is written to a file for an external boot flow to consume. It is
the BMC for a machine that does not have one: a QEMU guest, a container, a
lab box. It is equally a stand-in for developing and testing IPMI and
Redfish clients, with no hardware in the loop.

One process is one BMC. It runs in the foreground and never becomes the
parent of the machine it manages.

## Motivation

Existing virtual BMCs hard-wire their backend:
[virtualbmc](https://github.com/openstack/virtualbmc) requires libvirt,
[sushy-tools](https://github.com/openstack/sushy-tools) requires libvirt or
OpenStack, [kubevirtbmc](https://github.com/starbops/kubevirtbmc) requires
Kubernetes. Choosing the tool means choosing the virtualization stack. But the BMC
protocol frontend and the machine management backend are separate concerns,
and the narrowest interface between them is a command line.

Three projects reached for that interface independently:
[shipmi](https://github.com/lion7/shipmi) replaces virtualbmc's libvirt
driver with shell script providers,
[ipmi_sim](https://github.com/cminyard/openipmi) runs a configured
chassis_control program, and
[fakefish](https://github.com/openshift-metal3/fakefish) maps Redfish
operations to custom scripts. Each covers part of the picture: shipmi is a Python stack
serving IPMI only, fakefish serves Redfish only, and ipmi_sim owns the
machine process it controls. They also differ on how a command reports
power state: shipmi parses a status string from stdout, ipmi_sim reads a
parameter and value from stdout, and fakefish keeps no status command of
its own because it fronts a real BMC.

execbmc is the remaining intersection: a single static binary serving both
IPMI (RMCP+ and v1.5) and Redfish, with commands as the only backend
interface, running in the foreground without a management daemon. Where
those three differ, execbmc reads power state from the command exit code,
so a status check is an ordinary test command. The backend is whatever the
commands say: qemu-system started by a wrapper script, virsh start, docker
start, ssh to another host, or a command that models a slow or failing
machine for testing a client with no hardware in the loop.

## Usage

Write a JSON config file for the machine:

```json
{
  "name": "node1",
  "username": "admin",
  "password": "secret123",
  "ipmi": { "listen": "0.0.0.0:6230" },
  "redfish": { "listen": "0.0.0.0:8443" },
  "bootdev_path": "/var/lib/execbmc/node1.bootdev",
  "commands": {
    "power_status": "pgrep -f 'qemu-guest-node1'",
    "power_on": "/usr/local/bin/start-node1.sh",
    "power_off": "pkill -f 'qemu-guest-node1'"
  }
}
```

Start the BMC:

```
$ execbmc -config node1.json
{"time":"2026-07-18T05:49:20Z","level":"INFO","msg":"listening","component":"redfish","addr":"0.0.0.0:8443","tls":true}
{"time":"2026-07-18T05:49:20Z","level":"INFO","msg":"listening","component":"ipmi","addr":"0.0.0.0:6230"}
```

Operate it with ipmitool:

```
$ ipmitool -I lanplus -H 192.0.2.10 -p 6230 -U admin -P secret123 power status
Chassis Power is off
$ ipmitool -I lanplus -H 192.0.2.10 -p 6230 -U admin -P secret123 chassis bootdev pxe
Set Boot Device to pxe
$ ipmitool -I lanplus -H 192.0.2.10 -p 6230 -U admin -P secret123 power on
Chassis Power Control: Up/On
```

Or with the Redfish API:

```
$ curl -sk -u admin:secret123 -X PATCH \
    -d '{"Boot": {"BootSourceOverrideTarget": "Pxe", "BootSourceOverrideEnabled": "Once"}}' \
    https://192.0.2.10:8443/redfish/v1/Systems/1
$ curl -sk -u admin:secret123 -X POST \
    -d '{"ResetType": "On"}' \
    https://192.0.2.10:8443/redfish/v1/Systems/1/Actions/ComputerSystem.Reset
```

The machine can also be a command that changes over time, which makes
execbmc a controllable stand-in for developing a client. Here it reports off
for the first five seconds after power on:

```json
{
  "name": "node1",
  "username": "admin",
  "password": "secret123",
  "redfish": { "listen": "127.0.0.1:8443" },
  "bootdev_path": "/tmp/node1.bootdev",
  "commands": {
    "power_on": "date +%s > /tmp/node1.booted",
    "power_off": "rm -f /tmp/node1.booted",
    "power_status": "f=/tmp/node1.booted; test -f $f && [ $(($(date +%s) - $(cat $f))) -ge 5 ]"
  }
}
```

A client that polls PowerState until On now runs its real wait loop. Because
the machine is only a command, the same shape can model a failing BMC as
well, exercising a client's poll, timeout, and retry paths without hardware
in the loop.

### Command contract

Commands are run with `/bin/sh -c`, so quoting, pipes, and redirection
behave exactly as in a shell.

`power_status` follows the pgrep exit code convention: exit 0 means powered
on, exit 1 means powered off, any other exit code is an error.

`power_on` must detach the machine (setsid or equivalent) and return
promptly. execbmc waits only for the command to exit and never manages the
machine process. Stopping execbmc does not affect the machine's power state.

`power_soft`, `power_cycle`, and `reset` are optional. Without `power_soft`,
soft shutdown requests are rejected. Without `power_cycle`, execbmc powers
off, waits for `power_status` to report off, and powers on. Without `reset`,
hard reset requests perform a power cycle.

Each command runs under a timeout, set by `command_timeout_seconds` and
defaulting to 30 seconds. The same bound limits how long `power_cycle` waits
for `power_status` to report off, so raise it for a backend that is slow to
respond or to shut down.

These optional fields extend the config shown above. The optional commands
sit inside `commands` next to the required three, and
`command_timeout_seconds` is a top-level field:

```json
{
  "command_timeout_seconds": 60,
  "commands": {
    "power_soft": "/usr/local/bin/shutdown-node1.sh",
    "power_cycle": "/usr/local/bin/cycle-node1.sh",
    "reset": "/usr/local/bin/reset-node1.sh"
  }
}
```

### Boot device file

Setting a boot device writes a JSON document to `bootdev_path`:

```json
{"target": "pxe", "persistent": false}
```

`target` is one of `none`, `pxe`, `disk`, or `cdrom`. execbmc only writes
this file. The script that starts the machine reads it, translates the
target into boot arguments, and, when `persistent` is false, resets the file
to `{"target": "none", "persistent": false}` after one boot.

### Logging

Logs are JSON lines on stderr, one line per command execution, Redfish
request, and boot device change. IPMI is logged at the chassis operation
level: power and boot device operations appear, protocol-level reads such as
Get Device ID do not.

### Redfish endpoints

```
GET   /redfish/v1/
GET   /redfish/v1/Systems
GET   /redfish/v1/Systems/1
PATCH /redfish/v1/Systems/1
POST  /redfish/v1/Systems/1/Actions/ComputerSystem.Reset
```

The service root is unauthenticated, and everything else requires HTTP Basic
authentication. TLS is enabled by default with a certificate generated at
startup. Set `redfish.cert_file` and `redfish.key_file` to use your own, or
`"tls": false` to serve plain HTTP.

## Non-goals

- Managing or supervising machine processes
- SEL, sensors, SOL, FRU, virtual media
- Multiple systems per process
- Multiple users or Redfish sessions
- virtualbmc-style add/start/list management commands
- A substitute for validating against real hardware (vendor quirks, allowable values, actual timing)

## License

MIT License. See [LICENSE](./LICENSE) for details.
