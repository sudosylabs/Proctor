# Proctor

Proctor is an open-source, self-hosted examination and proctoring platform.
One logical installation represents one educational institution, whether it is
served by one application process or several.

> [!IMPORTANT]
> Proctor is under active pre-release development. It has not published a
> supported production release or compatibility window.

Proctor combines a Go server, a server-hosted React application, reusable Go
modules, and task-oriented public documentation. The product models academic
structure, exam authoring and publication, scheduled exam sittings, durable
exam attempts and workspaces, submissions, integrity review, authorization,
audit, and clustered operation without turning one installation into a
multi-tenant service.

## Documentation

- [Documentation workspace](docs/README.md)
- [Local development setup](docs/public/developers/local-setup.mdx)
- [Repository boundaries](docs/public/developers/repository-boundaries.mdx)
- [Security architecture](docs/public/security/index.mdx)
- [Server development guide](server/README.md)
- [Build and release guide](build/README.md)

## Start Developing

Use a current Git installation, the Go version declared by `go.work`, a
supported Node.js version, Docker with the Compose plugin, and a POSIX shell.
Then run:

```sh
make dev-tools
make bootstrap
make run-server
```

The complete setup guide explains supported host environments, generated local
state, synthetic development data, and verification:
[set up Proctor for local development](docs/public/developers/local-setup.mdx).

## Repository Structure

- `server/` owns Proctor domain behavior, application policy, persistence,
  transports, and runtime composition.
- `webapp/` contains the server-hosted browser application.
- `packages/cache`, `packages/mail`, and `packages/vfs` are independently
  versioned reusable Go modules.
- `docs/` contains public and API documentation plus the Docusaurus site.
- `build/` and the root `Makefile` own development, verification, packaging,
  and release workflows.

Run `make help` for the supported repository command surface.

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening an issue or pull
request. Participation in Proctor community spaces is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

Do not report vulnerabilities in public issues. Follow the private process in
[SECURITY.md](SECURITY.md).

## Licensing

The repository default is the GNU Affero General Public License, version 3
only. Reusable modules and some adapted files have explicit compatible license
exceptions. [LICENSING.md](LICENSING.md) is the authority for the complete
directory and file-level split.
