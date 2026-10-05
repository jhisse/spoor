# Capturing what an SDK sends

No key, no network: `fake.py` is a canned OpenAI, Anthropic and Gemini endpoint and an OTLP sink on one port. Needs `uv` and `node`.

```sh
npm install          # once, for vercel-ai.mjs
./all.sh             # every script, into out/<script>-<scenario>.pb and .json
```

To add an SDK:

1. Write `<name>.py` (every dependency pinned with `==` in its header, and `exclude-newer` for the transitive ones, as in the others) or `<name>.mjs`. Each script stands alone: it sets up the SDK's own instrumentation and makes the call. Endpoints and fake keys come from the environment `capture.sh` sets. The scenario (`plain`, `tool`, `error`) is the first argument.
2. Run `uv run fake.py` in one terminal and `./capture.sh <name>.py plain tool` in another, until `out/` has what the SDK really sends. One scenario must be one trace.
3. Add the line to `all.sh`, copy the captures worth keeping to `testdata/`, and give each a row in `internal/otlp/sdkcaptures_test.go` and `internal/web/goldencost_test.go`.

`./chatbots.sh` goes the other way: seven of the bots in `../chatbots` with real models through OpenRouter (`OPENROUTER_API_KEY`, a few cents), recorded by the same sink as `testdata/chatbot-*`. Their calls and tokens are the model's, so a new run changes the numbers in `internal/web/goldencost_test.go`.

The fake always reports the same usage (in `fake.py`), so costs can be checked by hand. The sink replaces the home directory in every string and drops the `host.*` and `process.*` resource attributes; read a capture before committing it anyway. `spoor demo` loads only the files named in `../embed.go`.
