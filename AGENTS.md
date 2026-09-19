# Agent notes for monty-go

Working notes for AI agents and contributors. Keep this file short and factual.

## Tests take ~5 minutes — do not kill them for taking too long

`go test ./...` runs the full embedded-WASM suite. It reports 160 passing tests
and subtests and takes roughly 280 seconds on an idle machine — considerably
longer on a loaded or shared box. A long run is normal and is not by itself
evidence of a hang.

- Never kill or time out a run on wall-clock duration alone.
- Pass a generous timeout: `go test ./... -count=1 -timeout 30m`. Go's default is
  10 minutes, which a loaded machine can exceed, producing a *false*
  `panic: test timed out` that looks like a real failure.
- To watch progress, redirect to a log and poll it instead of killing the run:

  ```bash
  go test ./... -count=1 -v > /tmp/test.log 2>&1 &
  grep -c '^--- PASS' /tmp/test.log   # progress so far
  ```

- Treat a run as hung only on direct evidence: a Go timeout goroutine dump, or a
  progress log that has stopped advancing entirely.

## Rebuilding the WASM artifact needs Rust 1.95+

`monty.wasm` is built from `crates/monty-wasm` and is committed, so a Go-only
change needs no Rust rebuild.

- monty v0.0.23 pulls `ruff_python_parser`, which uses let-chains, so the build
  requires Rust **1.95+**.
- `rust-toolchain.toml` pins that channel and the `wasm32-wasip1` target, so
  `make build` selects the right toolchain and rustup installs the target on
  first use. Do not remove it — without it `cargo build` falls back to the
  default toolchain, which is older here and fails on let-chains.
- `make build` rewrites `monty.wasm`, and the result is **not** byte-identical to
  the committed file: Rust embeds absolute `CARGO_HOME` paths in panic metadata,
  so the data section shifts by a few hundred bytes per machine. That is
  expected, not a bad build — the code section is the same size and the suite
  passes either way. Do not commit that churn.
- Keep `monty.wasm` mode `644` (it is data, not an executable) and let
  `.gitattributes` keep it `binary`.

## Keep the Go module tidy

`go mod tidy` must be a no-op. wazero is a **direct** dependency — it is imported
by `monty.go` and `wasm.go` — so it must not carry an `// indirect` marker.
