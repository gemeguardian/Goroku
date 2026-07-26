# deploy/

Production deployment artifacts. Currently:

- `goroku.service` — hardened systemd unit (non-root, sandboxed). See the
  header comment in the unit for hardening rationale and the public-bind /
  trusted-proxy note.

## Install (systemd)

```bash
# 1. binary (read-only tree)
install -d -m 755 /opt/goroku/bin
install -m 755 goroku /opt/goroku/bin/goroku

# 2. service account
groupadd --system goroku
useradd --system --gid goroku --home-dir /var/lib/goroku \
  --shell /usr/sbin/nologin goroku

# 3. data root (writable by the service only)
install -d -m 700 -o goroku -g goroku /var/lib/goroku

# 4. unit
install -m 644 goroku.service /etc/systemd/system/goroku.service
systemctl daemon-reload

# 5. optional env file (public bind / trusted proxies / debug)
#    install -m 600 -o goroku -g goroku /dev/null /etc/goroku/goroku.env
#    then uncomment EnvironmentFile in the unit

systemctl enable --now goroku
systemctl status goroku
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
```

Notes:

- The unit runs with `--no-git` so the process never performs destructive git
  operations. Rollback by replacing the binary, not `git reset`.
- Web panel binds to `127.0.0.1:8080` by default. To expose it, put it behind
  a reverse proxy and set `GOROKU_WEB_BIND` + `GOROKU_TRUSTED_PROXIES` in
  `/etc/goroku/goroku.env` (see [../SECURITY.md](../SECURITY.md)).
- Native plugin compilation (`.dlmod` / `.loadmod`) needs the Go toolchain on
  `PATH` and a writable `GOCACHE` under the data root. Pure binary installs
  without plugins do not need the toolchain.
