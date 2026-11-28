Run JavaScript against the DevEx Harness in one sandboxed call, instead of many separate tool calls. Pass `code`: an async arrow function, e.g.

    async () => {
      const gates = await events.recent({ ev: "gate", n: 3 });
      return gates.map(g => g.data.result);
    }

Each namespace below is a global; every method takes at most one object argument and returns a Promise. Call `codemode.describe("<namespace or path>")` for the TypeScript types and `codemode.search("<words>")` to find a method. Return only what you need: intermediate data stays in the sandbox and costs no tokens. `console.log` lines come back in `logs`.

The sandbox has no network, filesystem, timers or `require`; the connectors are the only way out. Limits: 30 s, 50 connector calls, ~6000 tokens of result. Connectors are read-only: record gates and costs with the `harness gate` / `harness cost` CLI, not from here.

To reuse a working program, pass `save_as` (and `description`); later code can call `await codemode.run("<name>", input)`, where the saved code is an `async (input) => …` function.

For questions about this codebase ("where is X", "what calls Y", reviewing an area), search the code graph before opening files. `graph.search` returns `{ mode, note?, hits }`. It ranks by embeddings ("semantic") when embeddings work, using the agent's LLM provider or a local Ollama if one is configured. Otherwise it uses keyword search and `note` says why. Pass `mode: "lexical"` for an exact identifier. For a multi-part question, search each part in the same `execute` call. Each hit carries `doc`, its doc comment and declaration line, which often answers the question outright. Otherwise read only the returned `file:line` spans, about 40 lines each, not whole files:

    async () => {
      const { hits } = await graph.search({ q: "where are budget alerts raised", k: 5 });
      const near = await graph.neighbors({ id: hits[0].id });
      return { hits: hits.map(h => `${h.symbol ?? h.label} ${h.file}:${h.line}`),
               calls: near.map(n => `${n.dir} ${n.relation} ${n.symbol ?? n.label} ${n.file}:${n.line}`) };
    }
