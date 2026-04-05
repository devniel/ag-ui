# AG-UI ADK Middleware (Go)

Bridges [Google ADK Go](https://google.golang.org/adk) agents to the [AG-UI protocol](https://github.com/ag-ui-protocol/ag-ui). Go port of the [Python `ag-ui-adk`](../python) middleware.

Provides an `http.Handler` that accepts AG-UI `RunAgentInput` requests, runs
a Google ADK Go agent via its runner, and streams the agent's output back
as AG-UI SSE events. Drop-in compatible with [CopilotKit](https://copilotkit.ai)'s
`HttpAgent` runtime adapter.

## Install

```bash
go get github.com/ag-ui-protocol/ag-ui/integrations/adk-middleware/go/pkg/aguiadk
```

## Usage

```go
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/ag-ui-protocol/ag-ui/integrations/adk-middleware/go/pkg/aguiadk"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func main() {
	ctx := context.Background()

	// 1. Create your ADK agent
	model, _ := gemini.NewModel(ctx, "gemini-2.5-flash", &genai.ClientConfig{
		APIKey: os.Getenv("GOOGLE_API_KEY"),
	})

	agent, _ := llmagent.New(llmagent.Config{
		Name:        "assistant",
		Model:       model,
		Description: "A helpful assistant.",
		Instruction: "You are a helpful assistant.",
	})

	// 2. Wrap it with the AG-UI handler
	handler, err := aguiadk.NewHandler(aguiadk.Config{
		Agent:          agent,
		SessionService: session.InMemoryService(),
	})
	if err != nil {
		log.Fatal(err)
	}

	// 3. Serve it over HTTP
	http.Handle("/", handler)
	log.Println("AG-UI ADK server listening on :8000")
	http.ListenAndServe(":8000", nil)
}
```

## Frontend (CopilotKit)

In your Next.js app's `/api/copilotkit/route.ts`:

```typescript
import {
  CopilotRuntime,
  ExperimentalEmptyAdapter,
  copilotRuntimeNextJSAppRouterEndpoint,
} from "@copilotkit/runtime";
import { HttpAgent } from "@ag-ui/client";

export const POST = async (req: NextRequest) => {
  const runtime = new CopilotRuntime({
    agents: {
      "assistant": new HttpAgent({ url: "http://localhost:8000/" }),
    },
  });

  const { handleRequest } = copilotRuntimeNextJSAppRouterEndpoint({
    runtime,
    serviceAdapter: new ExperimentalEmptyAdapter(),
    endpoint: "/api/copilotkit",
  });

  return handleRequest(req);
};
```

Then in your React app:

```tsx
<CopilotKit runtimeUrl="/api/copilotkit" agent="assistant">
  <CopilotChat />
</CopilotKit>
```

## How it works

The handler translates between AG-UI and ADK Go at two boundaries:

1. **Input**: AG-UI `RunAgentInput` JSON → ADK `genai.Content` + session state
2. **Output**: ADK `session.Event` stream → AG-UI SSE events

### Event mapping

| ADK event | AG-UI events |
|---|---|
| `Content.Parts[].Text` | `TEXT_MESSAGE_START` → `TEXT_MESSAGE_CONTENT` → `TEXT_MESSAGE_END` |
| `Content.Parts[].FunctionCall` | `TOOL_CALL_START` → `TOOL_CALL_ARGS` → `TOOL_CALL_END` |
| `Content.Parts[].FunctionResponse` | `TOOL_CALL_RESULT` |
| `Author == "user"` | (skipped — already sent by client) |

### Ordering invariants

The translator enforces AG-UI's protocol invariants:

1. Text messages are always framed: `START → CONTENT* → END`
2. Any open text stream is closed before emitting tool calls
3. Duplicate consolidated messages (non-streaming final after streamed partials) are suppressed

## Status

Community-maintained. Tracks the Python `ag-ui-adk` middleware for feature parity.

## License

Apache 2.0
