# rekam

Persistent memory for AI agents and the people building with them.

This repository contains the open Solo/Core edition of rekam: a local,
single-user memory server with SQLite storage, full-text search, revisions,
typed wiki-links, graph exploration, review scheduling, file attachments,
and MCP integration.

Your records stay on your machine. The server binds to loopback by default.

## Quick start

Download a release for your platform from the [GitHub Releases page](https://github.com/mausolusikreatif/rekam/releases), verify its checksum, then run:

```sh
rekam install
rekam serve
```

For development, install Go and Node.js, then:

```sh
npm --prefix frontend ci
make frontend-solo
go test -tags solo ./...
go build -tags solo -o dist/rekam ./cmd/rekam
```

## MCP

The Solo server supports MCP over stdio, Streamable HTTP, and SSE. Point a
local agent at `rekam serve --stdio` or at the local HTTP endpoint.

## Scope

Solo is intentionally single-user. Shared team workspaces, hosted accounts,
billing, and the managed control plane live in the private rekam-cloud
repository and are not part of this edition.

## License

The Solo/Core source is released under the MIT License. See [LICENSE](LICENSE).
