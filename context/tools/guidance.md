Run JavaScript against the DevEx Harness in one sandboxed call, instead of many separate tool calls. Pass `code`: an async arrow function, e.g.

    async () => {
      const gates = await events.recent({ ev: "gate", n: 3 });
      return gates.map(g => g.data.result);
    }

Each namespace below is a global; every method takes at most one object argument and returns a Promise. Call `codemode.describe("<namespace or path>")` for the TypeScript types and `codemode.search("<words>")` to find a method. Return only what you need: intermediate data stays in the sandbox and costs no tokens. `console.log` lines come back in `logs`.

The sandbox has no network, filesystem, timers or `require`; the connectors are the only way out. Limits: 30 s, 50 connector calls, ~6000 tokens of result. Connectors are read-only: record gates and costs with the `harness gate` / `harness cost` CLI, not from here.

To reuse a working program, pass `save_as` (and `description`); later code can call `await codemode.run("<name>", input)`, where the saved code is an `async (input) => …` function.
