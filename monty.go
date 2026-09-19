// Package montygo provides a Go wrapper around the Pydantic Monty Python
// interpreter compiled to WebAssembly. It uses wazero (pure Go, no CGO) to
// execute Python code in a sandboxed environment with pause/resume support
// for external function calls.
//
// The embedded module is monty v0.0.23, built from crates/monty-wasm as a core
// wasm32-wasip1 module rather than the WebAssembly Component Model build Monty
// ships for its JS package, since wazero does not implement the component
// model.
package montygo

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed monty.wasm
var montyWasm []byte

// Runner is a compiled Monty WASM runtime ready to execute Python code.
// Create one Runner and reuse it across multiple Execute calls.
// Each Execute call gets its own isolated WASM instance.
type Runner struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
}

// Limits configures resource limits for Python execution.
//
// A zero field means "no limit configured here", except that Monty itself
// defaults MaxRecursionDepth and MaxSuspensions to 1000 when they are left
// zero.
type Limits struct {
	// MaxMemoryBytes caps allocator-backed memory. Monty enforces it from inside
	// the allocator: the interpreter raises MemoryError once the session's soft
	// limit is crossed. A limit too large to express in a 32-bit address space
	// is ignored, since an uncapped wasm module has no tighter cap to offer.
	MaxMemoryBytes uint64 `json:"max_memory,omitempty"`
	// MaxDuration caps cumulative bytecode-execution time. Time spent suspended
	// in a host callback or OS call does not count against it.
	MaxDuration time.Duration `json:"-"`
	// MaxRecursionDepth caps Python call-stack depth; exceeding it raises
	// RecursionError.
	MaxRecursionDepth uint32 `json:"max_recursion_depth,omitempty"`
	// MaxSuspensions caps how many external function and OS calls the host will
	// service in one execution. Monty only stores this limit, so the bridge
	// enforces it and stops sandbox code that loops on host calls.
	MaxSuspensions uint32 `json:"max_suspensions,omitempty"`
}

// MarshalJSON implements custom JSON marshaling for Limits.
func (l Limits) MarshalJSON() ([]byte, error) {
	type alias struct {
		MaxDurationMs     *uint64 `json:"max_duration_ms,omitempty"`
		MaxMemory         *uint64 `json:"max_memory,omitempty"`
		MaxRecursionDepth *uint32 `json:"max_recursion_depth,omitempty"`
		MaxSuspensions    *uint32 `json:"max_suspensions,omitempty"`
	}
	a := alias{}
	if l.MaxDuration > 0 {
		v := uint64(l.MaxDuration.Milliseconds())
		a.MaxDurationMs = &v
	}
	if l.MaxMemoryBytes > 0 {
		v := l.MaxMemoryBytes
		a.MaxMemory = &v
	}
	if l.MaxRecursionDepth > 0 {
		v := l.MaxRecursionDepth
		a.MaxRecursionDepth = &v
	}
	if l.MaxSuspensions > 0 {
		v := l.MaxSuspensions
		a.MaxSuspensions = &v
	}
	return json.Marshal(a)
}

// FunctionCall contains information about an external function call from Python.
// Args contains all arguments merged into a single map — positional args are
// mapped to parameter names (registered via FuncDef) and kwargs are merged in.
type FunctionCall struct {
	Name   string
	Args   map[string]any
	CallID uint32
	// ObjectID is set when the call is routed to a host-backed object — a method
	// call on an instance, or construction of a host class — rather than a plain
	// external function. This bridge does not expose host objects, so it is
	// normally empty. The receiver is not included in Args.
	ObjectID string
}

// ArgsJSON returns Args serialized as a JSON string, suitable for passing
// directly to tool handlers that accept JSON argument strings.
func (fc *FunctionCall) ArgsJSON() string {
	if len(fc.Args) == 0 {
		return "{}"
	}
	b, err := json.Marshal(fc.Args)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// FuncDef defines an external Python function with its parameter names.
// Parameter names enable positional-to-keyword argument mapping in the WASM layer.
type FuncDef struct {
	Name   string   `json:"name"`
	Params []string `json:"params,omitempty"`
}

// Func creates a FuncDef with the given name and parameter names.
func Func(name string, params ...string) FuncDef {
	return FuncDef{Name: name, Params: params}
}

// OsCall contains information about an OS-level operation from Python.
type OsCall struct {
	Function string
	Args     []any
	Kwargs   map[string]any
	CallID   uint32
}

// ExternalFunc is called when Python code calls an external function.
type ExternalFunc func(ctx context.Context, call *FunctionCall) (any, error)

// OsCallFunc is called when Python code performs an OS operation.
type OsCallFunc func(ctx context.Context, call *OsCall) (any, error)

// ExecuteOption configures a single Execute call.
type ExecuteOption func(*executeConfig)

type executeConfig struct {
	externalFunc ExternalFunc
	osCallFunc   OsCallFunc
	limits       Limits
	printFunc    func(string)
	extFuncs     []FuncDef
}

// WithExternalFunc sets the callback for external function calls.
// Each FuncDef declares a function name and its parameter names (for
// positional-to-keyword argument mapping).
func WithExternalFunc(fn ExternalFunc, funcs ...FuncDef) ExecuteOption {
	return func(c *executeConfig) {
		c.externalFunc = fn
		c.extFuncs = funcs
	}
}

// declaresExtFunc reports whether name was declared through one of
// WithExternalFunc's FuncDefs. Callers that declare no functions keep the
// permissive behaviour of servicing whatever name the sandbox asks for.
func (c *executeConfig) declaresExtFunc(name string) bool {
	if len(c.extFuncs) == 0 {
		return true
	}
	for _, f := range c.extFuncs {
		if f.Name == name {
			return true
		}
	}
	return false
}

// WithOsCallFunc sets the callback for OS-level operations.
func WithOsCallFunc(fn OsCallFunc) ExecuteOption {
	return func(c *executeConfig) { c.osCallFunc = fn }
}

// WithLimits sets resource limits for the execution.
func WithLimits(l Limits) ExecuteOption {
	return func(c *executeConfig) { c.limits = l }
}

// WithPrintFunc sets a callback for Python print() output.
func WithPrintFunc(fn func(string)) ExecuteOption {
	return func(c *executeConfig) { c.printFunc = fn }
}

// New creates a new Monty WASM runner.
// The WASM module is compiled once and reused across Execute calls.
func New() (*Runner, error) {
	ctx := context.Background()
	config := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true)
	r := wazero.NewRuntimeWithConfig(ctx, config)

	// Instantiate WASI (provides clock, random, fd_write for the WASM module).
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("montygo: failed to instantiate WASI: %w", err)
	}

	compiled, err := r.CompileModule(ctx, montyWasm)
	if err != nil {
		r.Close(ctx)
		return nil, fmt.Errorf("montygo: failed to compile WASM module: %w", err)
	}

	return &Runner{
		runtime:  r,
		compiled: compiled,
	}, nil
}

// Execute runs Python code with the given inputs and returns the result.
// Each call creates an isolated WASM instance that is cleaned up when done.
func (r *Runner) Execute(ctx context.Context, code string, inputs map[string]any, opts ...ExecuteOption) (any, error) {
	cfg := &executeConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Instantiate a fresh module for this execution.
	mod, err := r.runtime.InstantiateModule(ctx, r.compiled,
		wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("montygo: failed to instantiate module: %w", err)
	}
	defer mod.Close(ctx)

	inst := &instance{mod: mod}
	if err := inst.resolveExports(); err != nil {
		return nil, err
	}

	return inst.execute(ctx, code, inputs, cfg)
}

// Close releases all WASM resources.
func (r *Runner) Close() error {
	return r.runtime.Close(context.Background())
}
