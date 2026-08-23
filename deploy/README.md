# Deploying Proctor

Release archives contain the server, compiled hosted web application, complete
configuration example, legal notices, build identity, and a hardened systemd
unit. The container image contains the same application payload and runs as an
unprivileged, read-only process.

## Systemd

1. Create the `proctor` system user and group.
2. Extract the release at `/opt/proctor` and keep it owned by root.
3. Copy `config/config.example.json` to `/etc/proctor/config.json`, set owner
   `root:proctor` and mode `0640`, and configure PostgreSQL, VFS, cache, mail,
   public URL, TLS, and secret values. The service's `proctor` user must be
   able to read the file while other users cannot.
4. Put sensitive environment overrides in `/etc/proctor/proctor.env` with mode
   `0600`; leave the file absent when JSON or secret-file integrations supply
   every value.
5. Install `deploy/systemd/proctor.service`, run `systemctl daemon-reload`, and
   enable the service.

The unit uses `Type=notify`. Proctor sends `READY=1` only after mandatory
dependencies, migrations, metrics, Jobs, HTTP, and internal readiness are all
usable. Put persistent ACME or local VFS state below `/var/lib/proctor` and file
logs below `/var/log/proctor`, or add an explicit systemd drop-in for another
operator-owned writable path.

## Container Compose example

The tracked `deploy/compose` example runs the immutable application container;
it intentionally does not disguise single-host PostgreSQL, Redis, or object
storage containers as a highly available production installation. Copy
`.env.example` to `.env`, `proctor.env.example` to `proctor.env`, and the
canonical server configuration example to `config.json`. Give `config.json`
owner `root:65532` and mode `0640`; keep `proctor.env` mode `0600`. Create
writable `data` and `logs` directories owned by uid/gid `65532`, and make any
mounted secret files readable by gid `65532` inside a private directory.
Replace `PROCTOR_IMAGE_REF` with the release's exact tag-and-digest reference
before running:

```sh
docker compose up --detach --wait
```

For active-active production, run one copy of this application service on each
host behind a redundant external load balancer. Give every node a stable
unique cluster ID, shared PostgreSQL/Redis/S3-compatible services, and the same
Memberlist keyring. Set `PROCTOR_CLUSTER_BIND_ADDRESS` in `.env` to that host's
private interface address so the mapped TCP and UDP port 7946 is reachable
only across the private cluster network. In `proctor.env`, configure the
Memberlist bind address as `0.0.0.0:7946`, advertise the host's reachable
private address, and enable `AllowPublicBind` only because Docker's host mapping
and firewall provide the actual private-interface boundary. Static seed
addresses, when used, must name those reachable host endpoints. Terminate
public TLS and HTTP-to-HTTPS forwarding at the redundant load balancer;
Proctor deliberately rejects node-local Let's Encrypt in Memberlist mode.

The Compose example binds HTTP and metrics to loopback by default. Exposing
metrics beyond loopback requires both TLS and bearer authentication in the
Proctor configuration. When the application listener itself uses built-in TLS,
set `PROCTOR_HEALTHCHECK_URL` to its local HTTPS URL and set
`PROCTOR_HEALTHCHECK_SERVER_NAME` to the certificate hostname. For a private
certificate authority, also set `PROCTOR_HEALTHCHECK_CA_FILE` to its path in
the mounted secrets directory.
