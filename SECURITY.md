# Security

## The model

1. spoor is single-tenant: one operator, one instance, on a network the operator controls.
2. **The UI port has no authentication, by design.** Reaching it means reading every prompt and completion, in the pages or through the MCP endpoint (`POST /mcp`), which is read-only.
3. **The ingest port has no authentication, by design.** Reaching it means writing traces. It cannot be used to read. A credential header is ignored.
4. spoor makes no outbound network call at runtime and sends no telemetry.
5. The database is one unencrypted SQLite file. Whoever can read the file can read everything in it.

## What spoor protects

- **Both ports bind loopback by default** (`127.0.0.1:8080`, `127.0.0.1:4318`). `spoor serve` logs a warning for each listener bound anywhere else. The Docker image listens on every interface inside the container; publish both ports on loopback (`-p 127.0.0.1:8080:8080 -p 127.0.0.1:4318:4318`).
- **Other sites in the operator's browser cannot read or change anything.** Every UI request except `/healthz` must carry a loopback `Host` or one listed in `SPOOR_ALLOWED_HOSTS` (this stops DNS rebinding). A request that is not GET or HEAD must also name that same host in `Origin` or `Referer` (this stops cross-site requests); `/mcp` alone accepts a request with no `Origin`, since an MCP client is not a browser. Pages cannot be framed (`Content-Security-Policy: frame-ancestors 'none'`).
- **`spoor export` and the MCP tools cannot write.** They use a connection SQLite itself keeps read-only.
- **A stored span is never overwritten** by a later request.

## What spoor does not protect

- **An exposed port.** There is no login and no ingest key. To reach spoor from another machine, put it behind a boundary you control: a VPN, a firewall, or a reverse proxy that authenticates. Do not expose either port to the internet.
- **Traffic in transit.** spoor serves plain HTTP. Terminate TLS in a proxy if traffic leaves the host.
- **Data at rest.** The database file and its backups are not encrypted. Use disk encryption and file permissions.
- **Secrets inside traces.** Prompts, completions and tool arguments are stored as received, with no redaction. Do not send what you would not store.
- **Ingestion abuse.** There is no rate limit. Whoever reaches the ingest port can fill the disk and can add spans to any trace id or service name, changing that trace's name, status and time range.
- **Isolation between services.** A service name groups traces. It is not an access boundary.

## Reporting a vulnerability

Report privately through GitHub: on the repository page, **Security → Report a vulnerability**. Do not open a public issue for something exploitable. Include the commit, how to reproduce, and what an attacker gains. spoor has one maintainer and no bug bounty.

In scope is anything that breaks the model above: reading through the ingest port, changing or deleting stored data through either port, injection through trace content rendered in the UI, SQL injection, path traversal in static assets, a write through a read-only command, or an outbound call that should not exist. The absence of authentication is the documented design, not a vulnerability.
