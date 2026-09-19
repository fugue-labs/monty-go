package montygo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// Status codes returned by the WASM shim.
const (
	statusError          = 0
	statusComplete       = 1
	statusFunctionCall   = 2
	statusOsCall         = 3
	statusResolveFutures = 4
)

// instance wraps a single WASM module instance for one execution.
type instance struct {
	mod       api.Module
	alloc     api.Function
	dealloc   api.Function
	compile   api.Function
	start     api.Function
	resume    api.Function
	resumeFut api.Function
	resLen    api.Function
	resRead   api.Function
	freeRun   api.Function
	freeSnap  api.Function
}

func (inst *instance) resolveExports() error {
	resolve := func(name string) (api.Function, error) {
		fn := inst.mod.ExportedFunction(name)
		if fn == nil {
			return nil, fmt.Errorf("montygo: WASM export %q not found", name)
		}
		return fn, nil
	}

	var err error
	if inst.alloc, err = resolve("wasm_alloc"); err != nil {
		return err
	}
	if inst.dealloc, err = resolve("wasm_dealloc"); err != nil {
		return err
	}
	if inst.compile, err = resolve("monty_compile"); err != nil {
		return err
	}
	if inst.start, err = resolve("monty_start"); err != nil {
		return err
	}
	if inst.resume, err = resolve("monty_resume"); err != nil {
		return err
	}
	if inst.resumeFut, err = resolve("monty_resume_futures"); err != nil {
		return err
	}
	if inst.resLen, err = resolve("monty_result_len"); err != nil {
		return err
	}
	if inst.resRead, err = resolve("monty_result_read"); err != nil {
		return err
	}
	if inst.freeRun, err = resolve("monty_free_runner"); err != nil {
		return err
	}
	if inst.freeSnap, err = resolve("monty_free_snapshot"); err != nil {
		return err
	}
	return nil
}

// writeBytes writes data to WASM memory and returns (ptr, len).
// Caller must free with freeWasmMem.
func (inst *instance) writeBytes(ctx context.Context, data []byte) (uint32, uint32, error) {
	length := uint32(len(data))
	if length == 0 {
		return 0, 0, nil
	}
	results, err := inst.alloc.Call(ctx, uint64(length))
	if err != nil {
		return 0, 0, fmt.Errorf("montygo: alloc failed: %w", err)
	}
	ptr := uint32(results[0])
	if ptr == 0 {
		return 0, 0, fmt.Errorf("montygo: alloc returned null for %d bytes", length)
	}
	if !inst.mod.Memory().Write(ptr, data) {
		return 0, 0, fmt.Errorf("montygo: memory write failed at %d len %d", ptr, length)
	}
	return ptr, length, nil
}

// writeString writes a string to WASM memory.
func (inst *instance) writeString(ctx context.Context, s string) (uint32, uint32, error) {
	return inst.writeBytes(ctx, []byte(s))
}

// writeJSON marshals value to JSON and writes to WASM memory.
func (inst *instance) writeJSON(ctx context.Context, v any) (uint32, uint32, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return 0, 0, fmt.Errorf("montygo: JSON marshal failed: %w", err)
	}
	return inst.writeBytes(ctx, data)
}

// freeWasmMem deallocates WASM memory.
func (inst *instance) freeWasmMem(ctx context.Context, ptr, length uint32) {
	if ptr != 0 && length != 0 {
		_, _ = inst.dealloc.Call(ctx, uint64(ptr), uint64(length))
	}
}

// readResult reads the internal result buffer from WASM.
func (inst *instance) readResult(ctx context.Context) ([]byte, error) {
	lenResults, err := inst.resLen.Call(ctx)
	if err != nil {
		return nil, fmt.Errorf("montygo: monty_result_len failed: %w", err)
	}
	length := uint32(lenResults[0])
	if length == 0 {
		return nil, nil
	}

	// Allocate a buffer in WASM memory to read result into.
	bufResults, err := inst.alloc.Call(ctx, uint64(length))
	if err != nil {
		return nil, fmt.Errorf("montygo: alloc for result buffer failed: %w", err)
	}
	bufPtr := uint32(bufResults[0])
	defer inst.freeWasmMem(ctx, bufPtr, length)

	_, err = inst.resRead.Call(ctx, uint64(bufPtr), uint64(length))
	if err != nil {
		return nil, fmt.Errorf("montygo: monty_result_read failed: %w", err)
	}

	data, ok := inst.mod.Memory().Read(bufPtr, length)
	if !ok {
		return nil, fmt.Errorf("montygo: memory read failed at %d len %d", bufPtr, length)
	}

	// Copy — WASM memory may be invalidated later.
	result := make([]byte, len(data))
	copy(result, data)
	return result, nil
}

// progressResult is the JSON structure returned by the WASM shim.
type progressResult struct {
	Status         string           `json:"status"`
	Value          *json.RawMessage `json:"value,omitempty"`
	SnapshotHandle *uint32          `json:"snapshot_handle,omitempty"`
	FunctionName   *string          `json:"function_name,omitempty"`
	OsFunction     *string          `json:"os_function,omitempty"`
	Args           *json.RawMessage `json:"args,omitempty"`
	Kwargs         *json.RawMessage `json:"kwargs,omitempty"`
	CallID         *uint32          `json:"call_id,omitempty"`
	ObjectID       *string          `json:"object_id,omitempty"`
	PendingCallIDs []uint32         `json:"pending_call_ids,omitempty"`
	Error          *string          `json:"error,omitempty"`
	PrintOutput    *string          `json:"print_output,omitempty"`
}

// execute runs the full compile->start->resume loop.
func (inst *instance) execute(ctx context.Context, code string, inputs map[string]any, cfg *executeConfig) (any, error) {
	// Build input names and values in order.
	var inputNames []string
	var inputValues []any
	for k, v := range inputs {
		inputNames = append(inputNames, k)
		inputValues = append(inputValues, v)
	}

	// 1. Compile the code.
	codePtr, codeLen, err := inst.writeString(ctx, code)
	if err != nil {
		return nil, err
	}
	defer inst.freeWasmMem(ctx, codePtr, codeLen)

	inputNamesPtr, inputNamesLen, err := inst.writeJSON(ctx, inputNames)
	if err != nil {
		return nil, err
	}
	defer inst.freeWasmMem(ctx, inputNamesPtr, inputNamesLen)

	extFuncNamesPtr, extFuncNamesLen, err := inst.writeJSON(ctx, cfg.extFuncs)
	if err != nil {
		return nil, err
	}
	defer inst.freeWasmMem(ctx, extFuncNamesPtr, extFuncNamesLen)

	compileResult, err := inst.compile.Call(ctx,
		uint64(codePtr), uint64(codeLen),
		uint64(inputNamesPtr), uint64(inputNamesLen),
		uint64(extFuncNamesPtr), uint64(extFuncNamesLen),
	)
	if err != nil {
		return nil, fmt.Errorf("montygo: monty_compile call failed: %w", err)
	}
	runnerHandle := uint32(compileResult[0])
	if runnerHandle == 0 {
		return nil, inst.readError(ctx)
	}
	defer func() { _, _ = inst.freeRun.Call(ctx, uint64(runnerHandle)) }()

	// 2. Start execution.
	inputsPtr, inputsLen, err := inst.writeJSON(ctx, inputValues)
	if err != nil {
		return nil, err
	}
	defer inst.freeWasmMem(ctx, inputsPtr, inputsLen)

	limitsPtr, limitsLen, err := inst.writeJSON(ctx, cfg.limits)
	if err != nil {
		return nil, err
	}
	defer inst.freeWasmMem(ctx, limitsPtr, limitsLen)

	startResult, err := inst.start.Call(ctx,
		uint64(runnerHandle),
		uint64(inputsPtr), uint64(inputsLen),
		uint64(limitsPtr), uint64(limitsLen),
	)
	if err != nil {
		return nil, fmt.Errorf("montygo: monty_start call failed: %w", err)
	}
	status := uint32(startResult[0])

	// 3. Loop on progress.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		result, readErr := inst.readResult(ctx)
		if readErr != nil {
			return nil, readErr
		}

		var progress progressResult
		if err := json.Unmarshal(result, &progress); err != nil {
			return nil, fmt.Errorf("montygo: failed to parse progress JSON: %w (raw: %s)", err, string(result))
		}

		// Deliver print output.
		if progress.PrintOutput != nil && cfg.printFunc != nil {
			cfg.printFunc(*progress.PrintOutput)
		}

		switch status {
		case statusComplete:
			if progress.Value == nil {
				return nil, nil
			}
			var val any
			if err := json.Unmarshal(*progress.Value, &val); err != nil {
				return nil, fmt.Errorf("montygo: failed to parse result value: %w", err)
			}
			return val, nil

		case statusError:
			errMsg := "unknown error"
			if progress.Error != nil {
				errMsg = *progress.Error
			}
			return nil, &MontyError{Message: errMsg}

		case statusFunctionCall:
			name := deref(progress.FunctionName)
			// Monty hands every unresolved global call to the host, so a name the
			// caller never declared is an undefined name from the sandbox's point of
			// view. Mirror the interpreter and raise NameError instead of leaking a
			// host-side error out of the sandbox.
			if cfg.externalFunc == nil || !cfg.declaresExtFunc(name) {
				return nil, &MontyError{Message: fmt.Sprintf("NameError: name '%s' is not defined", name)}
			}

			call := &FunctionCall{
				Name:     name,
				Args:     rawObjectToMap(progress.Args),
				CallID:   derefU32(progress.CallID),
				ObjectID: deref(progress.ObjectID),
			}

			returnVal, fnErr := cfg.externalFunc(ctx, call)
			if fnErr != nil {
				return nil, fmt.Errorf("montygo: external function %q failed: %w", call.Name, fnErr)
			}

			status, err = inst.resumeWithValue(ctx, derefU32(progress.SnapshotHandle), returnVal)
			if err != nil {
				return nil, err
			}

		case statusOsCall:
			if cfg.osCallFunc == nil {
				return nil, fmt.Errorf("montygo: OS call %q but no handler configured",
					deref(progress.OsFunction))
			}

			call := &OsCall{
				Function: deref(progress.OsFunction),
				Args:     rawArrayToAny(progress.Args),
				Kwargs:   rawObjectToMap(progress.Kwargs),
				CallID:   derefU32(progress.CallID),
			}

			returnVal, fnErr := cfg.osCallFunc(ctx, call)
			if fnErr != nil {
				return nil, fmt.Errorf("montygo: OS call %q failed: %w", call.Function, fnErr)
			}

			status, err = inst.resumeWithValue(ctx, derefU32(progress.SnapshotHandle), returnVal)
			if err != nil {
				return nil, err
			}

		case statusResolveFutures:
			return nil, fmt.Errorf("montygo: async futures not yet supported")

		default:
			return nil, fmt.Errorf("montygo: unknown status code %d", status)
		}
	}
}

// resumeWithValue serializes the return value and calls monty_resume.
func (inst *instance) resumeWithValue(ctx context.Context, snapshotHandle uint32, value any) (uint32, error) {
	valPtr, valLen, err := inst.writeJSON(ctx, value)
	if err != nil {
		return 0, err
	}
	defer inst.freeWasmMem(ctx, valPtr, valLen)

	results, err := inst.resume.Call(ctx,
		uint64(snapshotHandle),
		uint64(valPtr), uint64(valLen),
	)
	if err != nil {
		return 0, fmt.Errorf("montygo: monty_resume call failed: %w", err)
	}
	return uint32(results[0]), nil
}

// readError reads the result buffer and returns it as a MontyError.
func (inst *instance) readError(ctx context.Context) error {
	data, err := inst.readResult(ctx)
	if err != nil {
		return err
	}
	if data == nil {
		return &MontyError{Message: "unknown compilation error"}
	}
	var progress progressResult
	if err := json.Unmarshal(data, &progress); err != nil {
		return &MontyError{Message: string(data)}
	}
	if progress.Error != nil {
		return &MontyError{Message: *progress.Error}
	}
	return &MontyError{Message: "unknown compilation error"}
}

// Helper functions for dereferencing optional pointers.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefU32(v *uint32) uint32 {
	if v == nil {
		return 0
	}
	return *v
}

// rawArrayToAny converts a JSON array raw message to Go []any.
func rawArrayToAny(raw *json.RawMessage) []any {
	if raw == nil {
		return nil
	}
	var result []any
	_ = json.Unmarshal(*raw, &result)
	return result
}

// rawObjectToMap converts a JSON object raw message to Go map[string]any.
func rawObjectToMap(raw *json.RawMessage) map[string]any {
	if raw == nil {
		return nil
	}
	var result map[string]any
	_ = json.Unmarshal(*raw, &result)
	return result
}
