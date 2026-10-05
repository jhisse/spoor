# Working on spoor

The one file for whoever changes the code, person or agent. User-facing documentation is [README.md](README.md).

spoor is one Go binary that receives LLM traces over OTLP/HTTP, stores them in one SQLite file and shows them in a server-rendered web UI. It also answers a read-only MCP endpoint and a few read-only commands. **It stores, shows and counts. It does not judge.**

## Limits

A change that breaks one of these is a different project.

| Limit | Reason |
|---|---|
| One binary, no cgo | It is why spoor exists |
| One SQLite file | Backup is copying a file; the file is the read API |
| No outbound network call at runtime, no telemetry | Prices ship in the binary |
| MIT throughout, no `ee/` | |
| Traces only; OTLP is the only input | No spoor SDK, no mandatory spoor attribute |
| No login, no ingest key, no projects | One operator, zero configuration. Both listeners bind loopback by default |
| No custom JavaScript | htmx is the only script |
| At most 10,000 lines | See "The line budget" |

**Never:** evaluations, LLM-as-judge, datasets, experiments; prompt management or a playground; a gateway or proxy for model calls; RBAC, SSO, accounts; embedded models, embeddings, clustering; LLM-written summaries or an assistant; an alerting engine; a logs or metrics store; a hosted service; node-link graphs, autoplay replay, edit-and-rerun.

### Honesty rules

These are product behaviour, not style.

- Unknown is shown as unknown: no price means a NULL cost, shown as "–", never zero or a default. A token count the SDK did not send is "?".
- An estimate is labelled "estimated", permanently.
- With nothing to compare against, the page says so and draws nothing.
- What is hidden is counted: the page says how many rows and why.
- Signals are pointers ("worth a look"), never pass or fail.

### The line budget

At most **10,000 lines** of non-test Go, HTML templates and schema migrations (`cmd`, `internal`, `migrations`; tests and `storetest` do not count). `make loc` prints the count and fails above the limit; it is part of `make check`. A change that would exceed it removes something first.

### Deciding on a feature

Ask in order; stop at the first "no".

1. Does it store, show or count?
2. Does it respect every limit above?
3. Does it fit the line budget, or remove enough to fit?
4. Does it work on generic OTLP, without a spoor-specific attribute?
5. Is it deterministic?
6. Is there evidence someone needs it: a real trace that reads badly, a request, a study?

Six times "yes" and it is worth building. Welcome without that: bug fixes with a test that fails before the fix, real captured payloads for `testdata/` (secrets and personal data removed), recipes in `docs/recipes/`, documentation fixes.

## Commands

Go 1.27 or newer.

```sh
make build       # go build -o spoor ./cmd/spoor
make test        # go test ./... -race
make check       # gofmt, go vet, golangci-lint, gosec, govulncheck, loc, go test -race
make loc         # the line budget
make run         # build, then ./spoor serve
go test ./internal/config/... -run TestLoadDefaults -v    # one test

./spoor demo --http 127.0.0.1:18080 --ingest 127.0.0.1:14318    # the UI with the captures loaded
```

The linters are not vendored. Install the versions CI pins (`.github/workflows/ci.yml`):

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
go install github.com/securego/gosec/v2/cmd/gosec@v2.29.0
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
```

`spoor serve` needs no configuration; `migrate`, `export` and `reprice` need only `SPOOR_SQLITE_PATH`; `demo` takes flags and reads no variable. A `.env` in the working directory is read (`SPOOR_*` keys only) and never overrides a real variable.

## Releasing

Push a tag `vX.Y.Z`. `.github/workflows/release.yml` runs two jobs: GoReleaser (`.goreleaser.yaml`) attaches six archives (linux, darwin, windows × amd64, arm64) and `checksums.txt` to the GitHub release, and Docker buildx pushes a `linux/amd64,linux/arm64` image to `ghcr.io/jhisse/spoor` tagged `X.Y.Z`, `X.Y` and `latest`. The version reaches `spoor --version` through `-ldflags "-X main.version=…"`; a plain `go build` reports `dev`. The `Dockerfile` cross-compiles on the builder's architecture, so the image needs no emulation.

Try the whole thing without publishing: `go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean --skip=publish`, and `docker buildx build --platform linux/amd64,linux/arm64 .`.

## The map

Read in this order; each step explains the next.

| # | Where | What it is |
|---|---|---|
| 1 | `migrations/sqlite/000001_schema.up.sql` | The whole data model: `traces`, `spans`, `model_prices`, the `spans_fts` index, the `trace_summary` view. `internal/store/sqlite/model_prices.json` is the price catalog the binary loads into `model_prices` at every start |
| 2 | `internal/store/` | `store.go`: the `Store` interface and the domain types. `modelprice.go`: price matching, the token split, per-bucket cost |
| 3 | `cmd/spoor/` | Wiring. `main.go` dispatches `serve`, `demo`, `migrate`, `export`, `reprice`; `serve.go` holds the two servers, their routes and the retention loop; `mcp.go` is `POST /mcp` |
| 4 | `internal/config/` | Environment variables and `.env`. Nothing is required |
| 5 | `internal/otlp/` | Ingestion: `handler.go` → `decode.go` → `translate.go` + `messages.go` → `cost.go` → `assemble.go`. Also what a stored span's attributes mean (`toolspans.go`, `claudecode.go`, `events.go`) and the demo data (`demo.go`) |
| 6 | `internal/store/sqlite/` | The adapter: one file per table or query family; `scan.go` has the timestamp format; `readonly.go` opens the file read-only |
| 7 | `internal/web/` | One handler file per screen (`list.go`, `detail.go`, `sessions.go`, `blindspots.go`, `help.go`, `notfound.go`), pure view functions (`spantree.go`, `messages.go`, `metadata.go`, `treefold.go`, `readview.go`, `heading.go`) and chart builders (`tokenbar.go`, `charts.go`, `contextchart.go`, `heatmap.go`, `baseline.go`) |
| 8 | `internal/web/templates/`, `internal/web/static/spoor.css` | The screens and the one stylesheet |

Also: `internal/store/storetest/` is the conformance suite a `Store` must pass; `testdata/` holds the OTLP captures (`.pb` and decoded `.json`); `testdata/embed.go` names the ones `spoor demo` loads. `testdata/capture/` makes a capture from a real SDK with no key: a fake provider, an OTLP sink and one script per SDK. `testdata/chatbots/` is nine single-file chat bots (`uv run`) on different frameworks and instrumentations, for measuring spoor with a real key; eleven runs of them with real models are the `testdata/chatbot-*` captures (`testdata/capture/chatbots.sh`).

Dependencies point one way:

```
cmd/spoor ──► config, otlp, web, store, store/sqlite
web      ──► otlp, store
otlp     ──► store, testdata
sqlite   ──► store, migrations/sqlite
store    ──► nothing
```

Two HTTP servers run in one process. The ingest server only writes (`POST /v1/traces`; `/v1/logs` and `/v1/metrics` answer success and store nothing). The UI server only reads, apart from the theme cookie. Both serve `GET /healthz`.

## Invariants

Break one and something subtle goes wrong.

**Ingestion**

- **A trace is not an object on the wire.** The `traces` row is an aggregate spoor computes over the spans sharing a `trace_id`.
- **Span identity is `(trace_id, id)`**, and spans are first-write-wins (`ON CONFLICT DO NOTHING`). A retried span neither duplicates nor overwrites.
- **`MergeTrace` is the only writer of a trace row**, and it is one atomic upsert: earliest start, latest end, error sticky, the root's name, status and session replacing what is stored, a non-root batch only filling what is empty, the service being the first one any batch named. Never read-modify-write in Go: two batches of one trace arrive at the same time.
- **One OTLP request is one transaction** (`Store.IngestBatch`), all or nothing. A storage failure answers `503`, which OTLP senders retry; they do not retry `500`. A span with a malformed id is not a failure: the rest is stored and the count goes in `partial_success`.
- **A span is rejected only for a trace or span id of the wrong length.** Everything else is stored as sent. Validate at the boundary (HTTP input, OTLP payload, storage response) and nowhere else; no guard for a state the language already rules out.
- **`spans.trace_id` has no foreign key**: a span is written before its trace row. Retention therefore deletes traces first stored before the cutoff, then every span without a trace.

**Attributes and SQL**

- **Only `internal/otlp` names OTLP attributes** (`gen_ai.*`, `llm.*`, `session.id`, `service.name`, ...). The conventions are unstable. When another package needs a fact that lives in a stored span's attributes it asks `internal/otlp`: `Reprice`, `CostReported`, `PhaseOf`, `Purpose`, `ToolIO`, `FastMode`, `ExceptionOf`. User-facing text names a few (the MCP guide in `cmd/spoor/mcp.go`, the Help and Sessions pages, `reprice -h`); no code outside `internal/otlp` does.
- **Only `internal/store/sqlite` writes SQL.** The MCP tools and `spoor export` read through the `Store`, opened read-only (`sqlite.OpenReadOnly`), like the UI; spoor runs no SQL a user typed.
- **`Store` is the only port to data and grows one method at a time**, when something calls it. No generic repository, no layer between handlers and `Store`, no dependency injection framework. A new consumer is a delivery adapter over `Store`.
- **Regex matching and cost arithmetic run in Go**, not SQL.
- **`spans.attributes` keeps every attribute as sent.** `translateSpan` reads the columns from it and `metadata` is what is left over; the original is kept because the conventions are unstable and a changed reading must be applicable to stored data. The adapter compresses it (zlib), so it is opaque to SQL and a read without bodies leaves it out; `spoor export` shows it.

**Numbers**

- **Timestamps are fixed-width UTC text**, always nine fractional digits (`timeLayout` in `scan.go`), bound and scanned explicitly. Fixed width makes text order equal time order; every `ORDER BY`, cursor, time filter, `MIN`/`MAX` and retention cutoff depends on it.
- **`input_tokens` is the whole prompt**, cache included: `fresh = input_tokens − cache_read_tokens − cache_write_tokens`. `reasoning_tokens` is part of `output_tokens`. `normalizeInputTokens` (`translate.go`) documents how each SDK dialect is made to fit.
- **Every `model_prices` pattern is anchored (`^…$`).** A name no row matches has an unknown cost, never a neighbour's price. Name decorations (`[1m]`, date stamps, `5.5` for `5-5`) are normalised in one place, `store.modelNameForms`; change it and bump `matchVersion`. Longest pattern wins, alphabetical tiebreak.
- **Cost provenance**: `spans.price_pattern` and `price_fingerprint` record the row and its prices at the time. `serve` logs when stored fingerprints differ from today's table and never rewrites a cost; `spoor reprice` does. A cost the sender reported (`spoor.cost_usd`, `llm.cost.total`, LiteLLM's `gen_ai.cost.total_cost`) overrides the calculation and is never repriced. A known cost never becomes unknown.
- **A missing cache price is estimated** from the input price (read 0.1×, write 1.25×) and labelled. A cache write is priced at the 5-minute rate; no trace says which lifetime was used, so only the span's cost breakdown shows the 1-hour figure.
- **A cost priced without cache counts is marked `~`** in the list, the session totals, the trace header and the span panel, and explained in the span's cost breakdown: when the sender reports no cache count the whole prompt is priced as fresh input (`store.Span.CacheUnknown`).
- **What a model call did to its context is decided in one place per page kind**: `markSteps` for a trace (the trace page's chart and the list's rows), `sessionContexts.place` for a session (the session chart and the list's session cards). Both call `detectCacheBreak`; `buildContextSpark` only counts their marks. The list reads those calls with `SpanQuery.Usage`, which is answered from `idx_spans_trace_usage` alone: a column added to that select goes into the index too.
- **A parent that repeats its child's usage is counted once on the trace page** (`rollUp`). The list, the session totals and `trace_summary` still sum every span. An agent span is the exception: its usage is the total of the calls under it (the Vercel AI SDK's `invoke_agent`), so `translateSpan` leaves it in metadata, with no token columns and no cost.

**UI**

- **State lives in the URL**: selection (`?span=`), filters, pagination cursor, `?view=read`, `?fold=0`, `?y=`. There is no client state and no JSON API; handlers render HTML, and the `HX-Request` header chooses fragment or full page.
- **View logic is a pure function** from spans to a view model, with its own test and no HTTP or database dependency. Charts are server-rendered inline SVG whose geometry is computed in Go.
- **Every class in the markup is defined in `spoor.css`, and a `style` attribute may only set custom properties** (`style_test.go`). Colour is a class, geometry is an SVG attribute. A class that depends on a value is a fixed prefix plus the value (`k-{{.Kind}}`).
- **A span whose parent never arrived is shown as a root**, marked "no parent", never dropped.
- **`web.SameOrigin` wraps the whole UI server** (`origin.go`); the Host and Origin rules are in [SECURITY.md](SECURITY.md). There is no login: this is what stops a page in the operator's browser from reading prompts.
- **Every page model embeds `shell`** and is rendered through `renderPage`, which fills the header. Each page is parsed as `layout.html` plus its own file, because every page defines `content`.

## Traps

- **protojson mis-decodes OTLP/JSON ids.** OTLP/JSON sends `traceId`, `spanId` and `parentSpanId` as hex; `protojson` reads them as base64 and returns wrong bytes with no error. `decode.go` converts them first. Do not replace it with a direct `protojson.Unmarshal`.
- **`%v` on a pointer to a scalar prints an address.** Optional fields (`Span.Model`, `InputTokens`, `CostUSD`, `Trace.Service`, ...) go through `strVal`, `int64Val`, `costVal` in templates.
- **SQLite pragmas are DSN parameters**, never `db.Exec("PRAGMA …")`: `database/sql` pools connections and an `Exec` reaches one. `_txlock=immediate` is there because a deferred transaction that reads first fails with `SQLITE_BUSY` without waiting.
- **The driver re-formats `TIMESTAMP` columns on scan.** To see the stored text in a test, select `col || ''`.
- **`//go:embed` cannot reach outside its directory.** That is why `migrations/sqlite/embed.go` and `testdata/embed.go` exist. `./...` skips `testdata`, so that package is built only as a dependency.
- **Posting the same `.pb` twice changes nothing** (same ids, first write wins) and lands at the capture's date. `GET /sample.pb` serves a capture re-stamped to now under a fresh trace id; demo data is adjusted in `internal/otlp/demo.go`, never in the translation layer.
- **Claude Code exports the root span last**, so a turn is rootless while it runs. `TraceSummary.Rootless` marks it; `MergeTrace` takes the root's name when it arrives.
- **Claude Code's usage attributes are bare and disjoint** (`input_tokens` excludes cache). `fillBareInput` adds the cache counters for those names.
- **A tool's content is in its `tool.output` event**, not in an attribute. `otlp.ToolIO` reads it at render time, so it is not in `spans.input`/`output`; search, `spoor export` and MCP see it under `events`.
- **A tool span named without its tool gets the tool appended** at translation (`claude_code.tool` → `claude_code.tool Bash`).
- **Messages arrive in three forms**, and `messages.go` rebuilds each into the one array `web/messages.go` reads: flattened attributes (older OpenLLMetry's `gen_ai.prompt.N.role`, OpenInference's `llm.input_messages.N.message.role`), the OTel GenAI `parts` form in `gen_ai.input.messages` (current OpenLLMetry, the Vercel AI SDK, Pydantic AI, with `gen_ai.system_instructions` apart), and Gemini's own parts (`{"text": ...}`). Anything else is stored as sent.
- **Reasoning and inline media are not message text.** A `thinking` or `reasoning` part goes to `message.Reasoning` (the view shows it under a "Reasoning" disclosure); a blob part or a data-URL image becomes `[image: type, N bytes]` (`mediaNote`). The bytes stay in `spans.attributes`.
- **A message's `content` can be a JSON string of provider content blocks.** `web/messages.go` expands it only when every block has a known `type`; otherwise it shows the text.
- **The static export forbids external references**, and it inlines `spoor.css`: a comment in the stylesheet must not contain `<script`, `<link`, `src=`, `hx-`, or an `href`/`url(` that is not a `#fragment`.
- **Colour tokens are generated.** Edit the table in `docs/design/contrast.py`, run it (it fails under WCAG AA), paste the `--css` output at the top of `spoor.css`.
- **`govulncheck` checks the installed Go toolchain's standard library.** Findings "fixed in go1.x.y" mean a stale local Go, not a code issue; CI uses the latest stable.
- **CI runs on amd64, and arm64 fuses multiply-add.** A float computed on a Mac can differ in the last bit from the same code on CI (`stackRects` in `contextchart.go` prints `max(y, 0)` for that reason). Before pushing: `GOARCH=amd64 go test ./...` (Rosetta runs it).
- **`go.sum` lists gRPC packages** although spoor never speaks gRPC: the OTLP types module pulls them in.

## Tests and the pipeline

`make check` is the review. CI (`.github/workflows/ci.yml`) runs the same steps plus `docker build`, on amd64. Do not skip or weaken a step.

- **Test real scenarios only.** No test for a branch the compiler already rules out.
- **A test file is named after the source file it covers** (`baseline_test.go`), or after its subject when it covers several (`sdkcaptures_test.go`, `goldencost_test.go`, `smoke_test.go`). A package's shared test helpers live in one file: `helpers_test.go` in `otlp`, `web_test.go` in `web`.
- **Translation and views are tested on the captures in `testdata/`**, not on attribute names recalled from a specification. A new dialect comes with a capture; `testdata/capture/README.md` says how to make one.
- **`internal/store/storetest`** is written against the `Store` interface; `sqlite_test.go` runs it. A new `Store` method gets its case there.
- **Prices are tested against the table the binary loads** (`sqlite/pricetable_test.go`); `web/goldencost_test.go` prices every capture by hand, through to the HTML. A price change is an edit of `model_prices.json` plus those tables, in one commit.
- **`cmd/spoor/smoke_test.go`** runs the built binary: serve, ingest, render. `agent_test.go` covers `export` and the MCP endpoint.
- **A schema change is a new migration** in `migrations/sqlite/`, with [docs/schema.md](docs/schema.md) updated in the same commit: the schema is a public read API.
- **Pin every version, exactly.** Python dependencies with `==` and each script's `exclude-newer` (so the transitive ones stand still), npm packages, `uvx` tools, the Go version and toolchain, the CI tools, the runner image, Docker images (distroless by digest) and GitHub Actions, each at the latest release when added or touched (`gh api repos/<owner>/<repo>/releases/latest`, actions by commit SHA with the tag in a comment). A bump is its own change: rerun the captures and the tests with it, and where the newest release breaks something, say why in a comment (`testdata/capture/otel-openai-v2.py`).
- No new dependency for what a few lines can do. Comments only for an invariant or a workaround that is not obvious.

## Pointers

- [docs/schema.md](docs/schema.md): tables, columns, the `trace_summary` view, example queries for `sqlite3`.
- [docs/design.md](docs/design.md): the design system; `docs/design/contrast.py` generates and checks the colour tokens.
- [docs/recipes/](docs/recipes/): one page per sender, each saying what was tested.
- [SECURITY.md](SECURITY.md): what spoor protects and how to report.
