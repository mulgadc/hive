# teardown — Leak Catalog

Every task in this role corresponds to a known state source that survived a
previous reset. When the operator reports "reset didn't clean X", add a new
task and a new row here citing the symptom.

## Initial catalog (from reset-dev-env.sh + reset-dev-env.md target)

| State source | Cleaned by | Symptom if leaked |
|---|---|---|
| `spinifex.target` active | `systemctl stop` | Services keep running, file locks held |
| `spinifex-*` units in failed state | `systemctl reset-failed 'spinifex-*'` | `systemctl status` shows red units, next start refuses |
| Spinifex processes (spx, nats, predastore, etc.) | `pkill` pattern + wait loop | Stale listeners on 4222/8443/9999, start fails with "address in use" |
| QEMU VMs | `pkill qemu-system-*` + wait loop | Zombie VMs hold tap interfaces and viperblock state |
| `/etc/spinifex/` | `file: state=absent` | Stale TOML, stale CA, service users can't read fresh config |
| `/var/lib/spinifex/` | `file: state=absent` | Stale WAL, stale volumes, viperblock mounts fail |
| `/var/log/spinifex/` | `file: state=absent` | Logrotate confusion, permission drift after user changes |
| `/run/spinifex/` | `file: state=absent` | Stale sockets, pidfiles from prior systemd run |
| `$HOME/spinifex/` (legacy) | `file: state=absent` | Old dev-mode state shadowing new install |
| OVS bridges (all) | `ovs-vsctl del-br` loop | `br-int` populated with stale ports → `ovn-controller` commit loop |
| OVS `external_ids` | `ovs-vsctl clear Open_vSwitch .` | Stale chassis entries in SBDB |
| `/var/lib/ovn/ovnnb_db.db`, `ovnsb_db.db` | `file: state=absent` | "OVNSB commit failed, force recompute" loop after reset |
| `veth-wan-br` / `veth-wan-ovs` | `ip link delete` | Pair left dangling after setup-ovn.sh re-run |
| `spx-ext-*` macvlan interfaces | `ip link delete` loop | Stale macvlan on WAN NIC, packet duplication |
| Spinifex CA in trust store | remove + `update-ca-certificates` | TLS clients trust old CA, new cert fails verification |

## Not cleaned by default (opt-in)

| State source | Flag | Why opt-in |
|---|---|---|
| `~/.ssh/spinifex-key*` | `-e spinifex_wipe_ssh_keys=true` | Operator may want key to persist across resets |
| `~/.aws/credentials` spinifex profile | `-e spinifex_wipe_aws_creds=true` | `spx admin init --force` rewrites this block anyway; wipe only on demand |

## Known traps

- **`pgrep -f` self-match.** A literal pattern that matches the pattern
  string itself causes `pgrep -f` to match our own shell. Mitigation:
  bracket trick (`[b]in/spx` matches `bin/spx` but not `[b]in/spx`).
- **Log followers / ansible wrapper false positives.** `journalctl -f
  _SYSTEMD_UNIT=spinifex-*` and ansible's own module shell matches the
  service-name patterns in their cmdlines. Mitigation:
  `spinifex_process_exclude` filter drops `journalctl|ansible|/bin/sh|pgrep|pkill|tail -[fF]`.
- **SIGTERM survivors.** `spx service spinifex start` (and potentially
  other service-supervisor processes) ignore or delay SIGTERM. Mitigation:
  kill task escalates SIGTERM → SIGKILL after 5s.

## Adding a new entry

1. Add a task to `tasks/main.yml` in the right section (services / state-dirs /
   ovn / ovs / netlink / trust / user-owned).
2. Prefix the task `name:` with the state class (e.g. `netlink:`, `ovn:`).
3. Add a row to the catalog above — symptom is what the operator described.
4. If the leak class is general (e.g. "any interface matching spx-*"),
   generalise the task; do not pattern-match on a single instance name.
