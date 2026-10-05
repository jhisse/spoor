# Behind an OpenTelemetry Collector

## What you get

spoor as one more destination: your applications keep sending to the collector and your current backends keep receiving. It also gives gRPC-only senders a way in, since spoor itself accepts only OTLP/HTTP.

## Configuration

Add one exporter and list it in the traces pipeline:

```yaml
exporters:
  otlp_http/spoor:
    endpoint: http://<spoor-host>:4318      # no path; the exporter appends /v1/traces

service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [<your existing exporters>, otlp_http/spoor]
```

- No key or header is needed. The exporter's default gzip and protobuf are both accepted.
- Add the exporter to the **traces** pipeline only. spoor discards what arrives on `/v1/metrics` and `/v1/logs`.
- `<spoor-host>:4318` is spoor's `SPOOR_INGEST_ADDR`, which binds loopback by default. For a collector on another host set `SPOOR_INGEST_ADDR=:4318` and keep that port behind a firewall or VPN: it has no authentication.
- If the collector already owns port 4318 on the same host, move spoor, for example `SPOOR_INGEST_ADDR=127.0.0.1:4319`.
- Collector in Docker Desktop, spoor on the host: `http://host.docker.internal:<port>`.

Check it by posting a capture to the collector:

```sh
curl -X POST http://<collector-host>:4318/v1/traces \
  -H "Content-Type: application/x-protobuf" \
  --data-binary @testdata/openllmetry-openai-openrouter-tool-use.pb
```

The trace `openai.chat` appears in spoor within the collector's batch delay. If it does not, the collector log shows the HTTP status spoor returned.

## Tested

2026-10-05, `otel/opentelemetry-collector-contrib` 0.161.0 in Docker with exactly the configuration above (no header), spoor on the host bound to loopback and reached as `host.docker.internal`: a capture posted to the collector's OTLP/HTTP receiver appeared in spoor's trace list.

## Known gaps

- Collector 0.161.0 logs that the `otlphttp` type name is deprecated in favour of `otlp_http`. Both worked in that version; an older collector that does not know `otlp_http` needs `otlphttp`.
