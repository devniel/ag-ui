// Package aguiadk bridges Google ADK Go agents to the AG-UI protocol.
//
// It provides an HTTP handler that accepts AG-UI RunAgentInput requests,
// invokes a Google ADK agent via its runner, and streams the agent's output
// back as AG-UI SSE events.
//
// This is the Go equivalent of the Python ag-ui-adk middleware
// (https://github.com/ag-ui-protocol/ag-ui/tree/main/integrations/adk-middleware/python).
//
// Basic usage:
//
//	handler, err := aguiadk.NewHandler(aguiadk.Config{
//	    Agent:          myAdkAgent,
//	    SessionService: session.InMemoryService(),
//	    AppName:        "my_app",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	http.Handle("/", handler)
//	http.ListenAndServe(":8080", nil)
//
// Frontend setup with CopilotKit:
//
//	// Next.js /api/copilotkit/route.ts
//	const runtime = new CopilotRuntime({
//	  agents: {
//	    "my_agent": new HttpAgent({ url: "http://localhost:8080/" }),
//	  },
//	});
package aguiadk
