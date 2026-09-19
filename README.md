# monty-go

**Run LLM-generated Python safely from Go — no containers, no CGO, no subprocess.**

A pure-Go wrapper around [Pydantic's Monty](https://github.com/pydantic/monty) Python interpreter, compiled to WebAssembly and loaded via [wazero](https://wazero.io). Your Go agent writes Python code, monty-go executes it in a sandboxed WASM instance with sub-millisecond startup, and pauses whenever the code calls an external function so your Go code can handle it.

```bash
go get github.com/fugue-labs/monty-go
```

Wraps **monty v0.0.23**. The embedded interpreter is built from `crates/monty-wasm`, a thin C-ABI shim over Monty's pause/resume API — see [Building from Source](#building-from-source).

## Why?

LLMs work faster, cheaper, and more reliably when they write code instead of making sequential tool calls. Instead of:

```
Agent → tool_call("search", {query: "weather london"}) → result
Agent → tool_call("search", {query: "weather tokyo"})  → result
Agent → tool_call("compare", {a: result1, b: result2}) → result
```

The LLM writes:

```python
london = search(query="weather london")
tokyo = search(query="weather tokyo")
compare(a=london, b=tokyo)
```

One model call instead of three. The Python code calls your Go functions, Monty pauses at each call, your Go code executes it, and Monty resumes. No containers. No sandbox services. No `exec()`. Just a 5.0MB WASM binary embedded in your Go binary.

For motivation, see:
- [Programmatic Tool Calling](https://platform.claude.com/docs/en/agents-and-tools/tool-use/programmatic-tool-calling) from Anthropic
- [Code Execution with MCP](https://www.anthropic.com/engineering/code-execution-with-mcp) from Anthropic
- [Code Mode](https://blog.cloudflare.com/code-mode/) from Cloudflare
- [Smol Agents](https://github.com/huggingface/smolagents) from Hugging Face

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"

    montygo "github.com/fugue-labs/monty-go"
)

func main() {
    runner, err := montygo.New()
    if err != nil {
        log.Fatal(err)
    }
    defer runner.Close()

    result, err := runner.Execute(context.Background(),
        "x * 2 + y",
        map[string]any{"x": 10, "y": 5},
    )
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(result) // 25
}
```

## External Functions (Pause/Resume)

The real power is external function calls. Monty pauses execution whenever Python code calls a function you've declared, your Go callback handles it, and Monty resumes with the return value:

```go
result, err := runner.Execute(ctx,
    `
london = get_weather("London")
tokyo = get_weather("Tokyo")
f"{london['city']}: {london['temp']}°C, {tokyo['city']}: {tokyo['temp']}°C"
    `,
    nil,
    montygo.WithExternalFunc(func(ctx context.Context, call *montygo.FunctionCall) (any, error) {
        city, _ := call.Args["city"].(string)
        // Your real implementation here — HTTP call, database query, anything.
        return map[string]any{"city": city, "temp": 22}, nil
    }, montygo.Func("get_weather", "city")),
)
// result: "London: 22°C, Tokyo: 22°C"
```

Multiple functions work the same way — register them all and dispatch by name:

```go
result, err := runner.Execute(ctx, code, nil,
    montygo.WithExternalFunc(func(ctx context.Context, call *montygo.FunctionCall) (any, error) {
        switch call.Name {
        case "search":
            return doSearch(call.Args)
        case "calculate":
            return doCalculate(call.Args)
        case "store":
            return doStore(call.Args)
        default:
            return nil, fmt.Errorf("unknown function: %s", call.Name)
        }
    },
        montygo.Func("search", "query"),
        montygo.Func("calculate", "expression"),
        montygo.Func("store", "key", "value"),
    ),
)
```

## Resource Limits

Prevent runaway code with time, memory, recursion, and host-call limits:

```go
result, err := runner.Execute(ctx, code, inputs,
    montygo.WithLimits(montygo.Limits{
        MaxDuration:       5 * time.Second,
        MaxMemoryBytes:    10 * 1024 * 1024, // 10 MB
        MaxRecursionDepth: 100,
        MaxSuspensions:    1000,
    }),
)
```

Infinite loops, memory bombs, and deep recursion all terminate cleanly with a `*MontyError`.

- `MaxMemoryBytes` is enforced from inside the allocator, so it also catches memory that a single expression tries to
  allocate in one burst.
- `MaxDuration` counts bytecode execution only, so time your callbacks spend working does not eat into it.
- `MaxSuspensions` bounds how many external and OS calls the host will service. Because the execution-time budget is
  paused while you are handling a callback, this is what stops sandbox code that simply loops on host calls.

Go's `context.Context` deadlines are also respected — cancel the context and the WASM instance stops.

Note: monty removed its `max_allocations` limit, so `Limits.MaxAllocations` is gone; `MaxMemoryBytes` covers the
same ground.

## Print Capture

Capture Python `print()` output:

```go
var output strings.Builder
_, err := runner.Execute(ctx, `print("step 1 done")`, nil,
    montygo.WithPrintFunc(func(s string) { output.WriteString(s) }),
)
fmt.Print(output.String()) // "step 1 done\n"
```

## OS Calls

Python filesystem and environment access routes through your Go callback:

```go
result, err := runner.Execute(ctx,
    `
from pathlib import Path
data = Path("/config/settings.json").read_text()
data
    `,
    nil,
    montygo.WithOsCallFunc(func(ctx context.Context, call *montygo.OsCall) (any, error) {
        switch call.Function {
        case "Path.read_text":
            path, _ := call.Args[0].(string)
            return readFromYourStorage(path)
        case "Path.exists":
            path, _ := call.Args[0].(string)
            return existsInYourStorage(path), nil
        default:
            return nil, fmt.Errorf("blocked: %s", call.Function)
        }
    }),
)
```

No filesystem access happens unless your callback allows it.

## Gollem Integration

monty-go is designed to power **code-mode** in [Gollem](https://github.com/fugue-labs/gollem), the production agent framework for Go. Instead of sequential tool calls, the LLM writes Python that calls your tools as functions — Monty executes it safely, and Gollem orchestrates the whole thing.

Here's what this looks like with Gollem:

```go
import (
    "github.com/fugue-labs/gollem"
    "github.com/fugue-labs/gollem/provider/anthropic"
    montygo "github.com/fugue-labs/monty-go"
)

// Your existing Gollem tools — search, calculate, store, whatever.
searchTool := gollem.FuncTool[SearchParams]("search", "Search the knowledge base", doSearch)
calcTool := gollem.FuncTool[CalcParams]("calculate", "Run calculations", doCalc)

// Create a code-mode tool that wraps your toolset with Monty.
// The LLM writes Python code, Monty executes it, external function calls
// route to your Go tools.
codeMode := NewCodeModeTool(runner, searchTool, calcTool)

agent := gollem.NewAgent[Analysis](anthropic.New(),
    gollem.WithTools[Analysis](codeMode),
    gollem.WithSystemPrompt[Analysis](`You have a code execution tool.
Write Python code to call the available functions. Available functions:
- search(query: str) -> dict: Search the knowledge base
- calculate(expression: str) -> float: Evaluate math expressions
Write code that calls these functions and returns the result.`),
)

result, _ := agent.Run(ctx, "Compare Q3 and Q4 revenue and calculate the growth rate")
```

With one model call, the LLM writes:

```python
q3 = search(query="Q3 revenue")
q4 = search(query="Q4 revenue")
growth = calculate(expression=f"({q4['revenue']} - {q3['revenue']}) / {q3['revenue']} * 100")
{"q3": q3, "q4": q4, "growth_rate": growth}
```

Monty pauses three times (two searches, one calculation), your Go functions handle each one, and the final result flows back through Gollem's typed output pipeline. Three tool calls in one LLM round-trip.

Why Gollem + monty-go:

| | Traditional tool calling | Code-mode with monty-go |
|---|---|---|
| **LLM calls** | One per tool use | One for all tools |
| **Latency** | N × model round-trip | 1 × model round-trip + μs execution |
| **Cost** | N × input/output tokens | 1 × input/output tokens |
| **Logic** | LLM reasons step by step | LLM writes the logic once |
| **Control flow** | None (sequential only) | Loops, conditionals, variables |
| **Error handling** | LLM must react to each failure | try/except in Python |
| **Security** | ✅ (tools are Go functions) | ✅ (WASM sandbox + your callbacks) |

Gollem gives you compile-time type safety, structured output, guardrails, cost tracking, middleware, and multi-provider support. monty-go gives you secure embedded Python execution. Together, your agents do more work per model call.

**[github.com/fugue-labs/gollem](https://github.com/fugue-labs/gollem)** — The production agent framework for Go.

## How It Works

```
┌─────────────────────────────────────────────────────────┐
│  Your Go Application                                    │
│                                                         │
│  runner, _ := montygo.New()                             │
│  result, _ := runner.Execute(ctx, code, inputs, opts)   │
│       │                                                 │
│       ▼                                                 │
│  ┌──────────────────────────────────┐                   │
│  │  wazero (pure Go WASM runtime)  │                    │
│  │                                 │                    │
│  │  ┌───────────────────────────┐  │                    │
│  │  │  monty.wasm (5.0 MB)      │  │  ◄── go:embed      │
│  │  │  Monty Python Interpreter │  │                    │
│  │  │  compiled to wasm32-wasip1│  │                    │
│  │  └──────────┬────────────────┘  │                    │
│  │             │                   │                    │
│  │     pause on external call      │                    │
│  │             │                   │                    │
│  └─────────────┼───────────────────┘                    │
│                │                                        │
│                ▼                                        │
│  ExternalFunc callback ──► your Go code ──► resume      │
│  OsCallFunc callback   ──► your Go code ──► resume      │
│  PrintFunc callback    ──► your Go code                 │
└─────────────────────────────────────────────────────────┘
```

- **No CGO.** wazero is a pure-Go WebAssembly runtime.
- **No subprocess.** The WASM binary is embedded via `go:embed` and compiled once at startup.
- **Fresh instance per call.** Each `Execute()` gets an isolated WASM instance. No state leaks between calls.
- **JSON at the boundary.** All data crossing the Go↔WASM boundary is JSON. Go types map naturally: `int`→`float64`, `string`→`string`, `bool`→`bool`, `nil`→`None`, `[]any`→`list`, `map[string]any`→`dict`.

## API

```go
// Create a reusable runner. Compiles the WASM module once.
runner, err := montygo.New()
defer runner.Close()

// Execute Python code with inputs and options.
result, err := runner.Execute(ctx, code, inputs, opts...)

// Options:
montygo.WithExternalFunc(fn,                     // register callable functions
    montygo.Func("search", "query", "limit"),    // with named parameters
    montygo.Func("calculate", "expression"),
)
montygo.WithOsCallFunc(fn)                       // handle filesystem/env access
montygo.WithLimits(montygo.Limits{...})          // resource limits
montygo.WithPrintFunc(fn)                        // capture print output

// FunctionCall provides named args (positional mapped by param name):
call.Args["query"].(string)    // access by parameter name
call.ArgsJSON()                // pre-serialized JSON string
```

### Types

| Python | Go (result) | Go (input) |
|--------|------------|------------|
| `int` | `float64` | `int`, `float64` |
| `float` | `float64` | `float64` |
| `str` | `string` | `string` |
| `bool` | `bool` | `bool` |
| `None` | `nil` | `nil` |
| `list`, `tuple` | `[]any` | `[]any` |
| `dict` | `map[string]any` | `map[string]any` |
| `set` | `[]any` | — |

### Errors

Python exceptions become `*montygo.MontyError`:

```go
result, err := runner.Execute(ctx, "1 / 0", nil)
var me *montygo.MontyError
if errors.As(err, &me) {
    fmt.Println(me.Message) // "Traceback... ZeroDivisionError: division by zero"
}
```

## What Monty Can Do

Monty implements the subset of Python that a model needs, and aims to match CPython 3.14 everywhere it does implement
something. Upstream's `limitations/` docs are the exhaustive record; this is the practical summary.

- Arithmetic, strings, f-strings (including the `=` debug form), `str.format()`, slicing
- Functions, `lambda`, closures, decorators, `async def` with `asyncio.run` / `asyncio.gather`
- Simple classes: methods, `__init__`, dunder protocols, class variables, `with` blocks
- `for`/`while` loops, `if`/`elif`/`else`, `break`/`continue`
- `try`/`except`/`else`/`finally`, `raise ... from ...`, exception hierarchy
- List/dict/set comprehensions
- `range`, `len`, `sum`, `min`, `max`, `sorted`, `reversed`, `enumerate`, `zip`, `map`, `filter`, `all`, `any`
- `isinstance`, `type`, `int()`, `float()`, `str()`, `bool()`, `abs()`
- `dataclass` (`eq=` and `frozen=` only), `collections.namedtuple`
- Modules: `asyncio`, `base64`, `binascii`, `collections`, `dataclasses`, `datetime`, `functools`, `itertools`, `json`, `math`, `os`, `pathlib`, `re`, `sys`, `typing`, `unicodedata`
- `print()` with `sep` and `end` kwargs
- `import os`, `from pathlib import Path` (routed through OsCallFunc)
- Resource limits: time, memory, recursion depth, host-call budget

## What Monty Cannot Do

Monty deliberately stops short of full Python. The things most likely to bite:

- Class inheritance, metaclasses and method decorators — so no `super()`, `property`, `classmethod` or `staticmethod`
- `yield` / generator functions (generator expressions parse but materialise to a `list`)
- `match` statements, `del`, exception groups (`except*`), PEP 695 `type` aliases, `async with` / `async for`
- User-defined exception classes
- Runtime code execution: `eval`, `exec`, `compile`, `__import__`
- Introspection: `globals`, `locals`, `vars`, `dir`, and function attributes such as `fn.__name__`
- `property`, `classmethod`, `staticmethod`, `callable`, `issubclass`, `delattr`
- Modules outside the fixed list above, and any third-party library — there is no `sys.path`
- `enumerate`, `zip`, `map`, `filter` and `reversed` are eager, not lazy

Two entries in this list have changed upstream and no longer apply: simple classes are supported now, and `filter()`
works. `float('inf')` and `float('nan')` still do not survive the JSON boundary into Go.

## Tests

160 end-to-end tests covering every testable scenario from Monty's core test suite:

```bash
make test
```

Covers: basic expressions, print variants, all exception types, data type round-tripping, external functions (args, kwargs, mixed, complex types, chaining, loops), input handling and scoping, resource limits (timeout, recursion, memory, host-call budget), OS calls, builtins, control flow, lambdas/closures, and execution isolation.

## Building from Source

Requires **Rust 1.95+** with the `wasm32-wasip1` target, and Go 1.25+. The Rust floor is set by monty v0.0.23,
which compiles `ruff_python_parser` — that crate uses `if let` guards, stabilized in 1.95.

```bash
rustup toolchain install 1.95 --profile minimal --target wasm32-wasip1
make build  # compiles Rust → WASM, copies to monty.wasm
make test   # builds and runs Go tests
```

The artifact is a **core** WASM module, not the WebAssembly Component Model build Monty ships for its JS package:
wazero does not implement the component model, so `crates/monty-wasm` exposes Monty's pause/resume API over a plain
C ABI instead. To move to a newer Monty release, bump the `monty`, `monty-types` and `monty-alloc` pins in
`crates/monty-wasm/Cargo.toml` together (they must share one revision, so that they see the same memory accounting),
rebuild, and re-run the tests.

## Acknowledgments

monty-go exists because of [Monty](https://github.com/pydantic/monty), created by [Samuel Colvin](https://github.com/samuelcolvin) and the [Pydantic](https://github.com/pydantic) team. Monty is a genuinely novel piece of engineering — a minimal, secure Python interpreter written from scratch in Rust, purpose-built for AI agents. The insight that LLMs should write code instead of making sequential tool calls, and that you need a safe interpreter (not a container) to execute it, is what makes code-mode possible.

Samuel and the Pydantic team have a track record of building foundational tools that the whole ecosystem builds on — [Pydantic](https://github.com/pydantic/pydantic), [Pydantic AI](https://github.com/pydantic/pydantic-ai), [Logfire](https://github.com/pydantic/logfire), and now Monty. This project is a Go bridge to their work, and we're grateful they built it.

## License

MIT
