# /// script
# requires-python = ">=3.12,<3.14"
# dependencies = ["crewai==1.15.23", "openinference-instrumentation-crewai==1.1.20", "openinference-instrumentation-openai==0.1.63", "openai==2.54.0", "opentelemetry-sdk==1.45.0", "opentelemetry-exporter-otlp-proto-http==1.45.0"]
# [tool.uv]
# exclude-newer = "2026-10-05T03:23:43Z"
# ///
# CrewAI calls OpenAI through its own client, so the crew's agents come from the CrewAI
# instrumentor and the model calls from the OpenAI one, as in a real application.
import os

os.environ["CREWAI_DISABLE_TELEMETRY"] = "true"  # its own telemetry would go to CrewAI's servers

from crewai import Agent, Crew, Task
from openinference.instrumentation import using_session
from openinference.instrumentation.crewai import CrewAIInstrumentor
from openinference.instrumentation.openai import OpenAIInstrumentor
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor

provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(provider)
CrewAIInstrumentor().instrument(tracer_provider=provider)
OpenAIInstrumentor().instrument(tracer_provider=provider)

researcher = Agent(role="Researcher", goal="Answer the question", backstory="You research.", llm="gpt-4o-mini", allow_delegation=False)
writer = Agent(role="Writer", goal="Write a headline", backstory="You write.", llm="gpt-4o-mini", allow_delegation=False)
answer = Task(description="What is the capital of France?", expected_output="One word.", agent=researcher)
headline = Task(description="Turn the answer into a headline.", expected_output="One word.", agent=writer, context=[answer])
with using_session("capture-session"):
    Crew(agents=[researcher, writer], tasks=[answer, headline]).kickoff()
