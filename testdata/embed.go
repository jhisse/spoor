// Package testdata embeds the captured OTLP payloads `spoor demo` loads and
// GET /sample.pb serves (about 85 KB), so both work from the binary alone.
// The demo is this list: the other captures in the directory are for tests.
package testdata

import "embed"

//go:embed claude-code-*.pb openllmetry-*.pb openinference-openai-agents-tool.pb openinference-crewai-workflow.pb pydantic-ai-tool.pb
var FS embed.FS
