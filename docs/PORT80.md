# Port 80 and replacing the ASL3 web apps

Status: **not decided**. Notes to revisit; nothing here is built.

## Goal

Run ASLCP on port 80 (`http://node/` with no port), and in time cover what
operators open the other ASL3 web apps for, so they can be turned off.

What the ASL3 image runs today:

| Port | What | Used for |
|------|------|----------|
| 80 | Apache: Allmon3 (`/allmon3`) and the ASL3 landing page | node status, often a public status link |
| 9090 | Cockpit (with the ASL3 plugin) | browser terminal, updates, logs, network |
| 8080 | ASLCP (default) | |

Because 9090 is Cockpit's port, ASLCP's docs use 8181 as the example port.

## Why `ASLCP_PORT=80` fails today

1. **Apache holds port 80.** The installer warns about the clash and ASLCP
   does not start.
2. **ASLCP may not bind a port below 1024.** The service runs as the `aslcp`
   user with `CapabilityBoundingSet=` (no capabilities).
   `packaging/systemd/aslcp.service` documents a manual override with
   `AmbientCapabilities=CAP_NET_BIND_SERVICE`, but nothing sets it up.

## Options

### A. ASLCP owns port 80 (preferred)

- When `ASLCP_PORT` (or `listen`) is below 1024, the installer and the package
  add a drop-in granting only `CAP_NET_BIND_SERVICE` (bind low ports, nothing
  else). It is removed when the port goes back to 1024 or above.
- If Apache holds the port: offer to stop and disable it, or refuse with a
  clear message unless the operator opts in (for example `ASLCP_TAKE_PORT=1`).
  Disable, do not purge: `systemctl enable --now apache2` brings Allmon3 and the
  landing page back.
- Check first that nothing else on the node uses Apache.

### B. Apache stays on 80 and proxies to ASLCP

- ASLCP at `/`, Allmon3 stays at `/allmon3`; nothing is lost.
- Costs: ASLCP depends on Apache's config, the live updates need WebSocket
  proxy settings, and an ASL3 update to Apache's config can break it.

## Turning off the ASL3 web apps

An opt-in, per-service on/off switch is fine ethically and a security gain
(fewer listening web apps), if it is done this way:

- **Opt-in only.** Never during install or update. A labelled option with a
  confirmation that lists exactly what changes.
- **Neutral wording.** The ASL3 team ships these on purpose and many operators
  rely on them. For example: "Cockpit (port 9090) and Allmon3 (Apache, port 80)
  come with the ASL3 image. ASLCP covers much of what they do. Turn them off if
  you don't use them."
- **Disable, don't remove.** On the Pi image they are probably pulled in by the
  appliance meta-package (check on a node; something like
  `asl3-pi-appliance`). Purging them would remove the meta-package, so later
  ASL3 updates could miss changes and the node would no longer match the
  standard image when asking for help. `systemctl disable --now cockpit.socket
  apache2` gives the same security result and undoes in one click.
- **Say what is lost.** Cockpit is the fallback way in (terminal, updates, logs)
  if ASLCP ever breaks; Allmon3 may be what a public status link points to.
- **Show the state** (running, off) and confirm the ports are closed.

## Gaps to fill first

- **WiFi manager.** `nmcli` behind the helper: scan, connect, forget, hotspot
  fallback. Low risk; passwords are write-only, like the DVSwitch password.
- **Terminal.** A browser root shell is the most powerful thing the panel
  would expose: admin only, password asked again right before opening, each
  session logged, probably off until enabled.

## Next steps when revisited

1. On a real ASL3 node, check which packages pull in Cockpit, Allmon3 and
   Apache, and what else uses Apache.
2. Pick option A or B.
3. Decide the order: port 80, WiFi manager, terminal (each its own `try/*`
   branch).
