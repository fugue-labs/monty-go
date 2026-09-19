//! WASM shim for the Monty Python interpreter.
//!
//! Exposes Monty's iterative (pause/resume) execution API as C-ABI WASM
//! exports for the Go bridge, which drives the module with wazero.
//!
//! The artifact is a **core** WASM module (wasm32-wasip1) rather than the
//! WebAssembly Component Model build Monty ships for its JS package, because
//! wazero does not implement the component model.
//!
//! State (compiled runners, suspended continuations) lives in globals keyed by
//! handles. That is safe because a WASM instance is single-threaded and the Go
//! side instantiates a fresh module per Execute call.
//!
//! Targets monty v0.0.23.

use std::alloc::{alloc, dealloc, Layout};
use std::collections::HashMap;
use std::sync::Mutex;
use std::time::Duration;

use monty::{FunctionCall, MontyRun, OsCall, ResolveFutures, RunProgress};
use monty_types::{
    CompileOptions, ExcType, ExtFunctionResult, MontyException, MontyObject, NameLookupResult, PrintWriter,
    ResourceLimits, ResourceTracker,
};
use serde::{Deserialize, Serialize};
use serde_json::Value as JsonValue;

// ---------------------------------------------------------------------------
// Memory limit enforcement
// ---------------------------------------------------------------------------

/// Monty enforces max_memory from inside the allocator: the interpreter reads
/// real usage at execution checkpoints and raises MemoryError once the
/// session's soft limit is crossed. Arming that limit requires monty-alloc to be
/// this module's global allocator — set_limit refuses to arm otherwise, and
/// max_memory would then be silently ignored.
///
/// Exceeding the hard ceiling (soft limit plus headroom) cannot raise a Python
/// exception, so on wasm it traps the instance; wazero reports that as a trap
/// error, and the Go side's per-Execute instance is discarded anyway.
#[global_allocator]
static ALLOC: monty_alloc::LimitedAllocator = monty_alloc::LimitedAllocator;

// ---------------------------------------------------------------------------
// MontyObject <-> serde_json::Value conversion
// ---------------------------------------------------------------------------

/// Convert MontyObject to a plain JSON value (not the tagged enum format).
fn monty_to_json(obj: &MontyObject) -> JsonValue {
    match obj {
        MontyObject::None => JsonValue::Null,
        MontyObject::Bool(b) => JsonValue::Bool(*b),
        MontyObject::Int(i) => serde_json::json!(*i),
        MontyObject::BigInt(bi) => {
            // Try to fit in i64, fall back to string
            if let Ok(v) = i64::try_from(bi) {
                serde_json::json!(v)
            } else {
                JsonValue::String(bi.to_string())
            }
        }
        MontyObject::Float(f) => serde_json::json!(*f),
        MontyObject::String(s) => JsonValue::String(s.clone()),
        MontyObject::Bytes(b) => {
            // Encode as array of ints
            JsonValue::Array(b.iter().map(|byte| serde_json::json!(*byte)).collect())
        }
        MontyObject::List(items) => JsonValue::Array(items.iter().map(monty_to_json).collect()),
        MontyObject::Tuple(items) => JsonValue::Array(items.iter().map(monty_to_json).collect()),
        MontyObject::Dict(pairs) => {
            let mut map = serde_json::Map::new();
            for (k, v) in pairs {
                let key = match k {
                    MontyObject::String(s) => s.clone(),
                    other => format!("{other:?}"),
                };
                map.insert(key, monty_to_json(v));
            }
            JsonValue::Object(map)
        }
        MontyObject::Set(items) | MontyObject::FrozenSet(items) => {
            JsonValue::Array(items.iter().map(monty_to_json).collect())
        }
        MontyObject::Ellipsis => JsonValue::String("...".to_owned()),
        MontyObject::Path(p) => JsonValue::String(p.clone()),
        MontyObject::Exception { exc_type, arg } => {
            let mut map = serde_json::Map::new();
            map.insert("exception".to_owned(), JsonValue::String(format!("{exc_type:?}")));
            if let Some(a) = arg {
                map.insert("message".to_owned(), JsonValue::String(a.clone()));
            }
            JsonValue::Object(map)
        }
        MontyObject::NamedTuple {
            type_name,
            field_names,
            values,
        } => {
            let mut map = serde_json::Map::new();
            map.insert("__type__".to_owned(), JsonValue::String(type_name.clone()));
            for (name, val) in field_names.iter().zip(values.iter()) {
                map.insert(name.clone(), monty_to_json(val));
            }
            JsonValue::Object(map)
        }
        // Fallback for the remaining variants (datetime types, host objects,
        // opaque reprs): their Display form is the closest plain-JSON analogue.
        other => JsonValue::String(other.to_string()),
    }
}

/// Convert a plain JSON value to a MontyObject.
fn json_to_monty(val: &JsonValue) -> MontyObject {
    match val {
        JsonValue::Null => MontyObject::None,
        JsonValue::Bool(b) => MontyObject::Bool(*b),
        JsonValue::Number(n) => {
            if let Some(i) = n.as_i64() {
                MontyObject::Int(i)
            } else if let Some(f) = n.as_f64() {
                MontyObject::Float(f)
            } else {
                MontyObject::None
            }
        }
        JsonValue::String(s) => MontyObject::String(s.clone()),
        JsonValue::Array(items) => MontyObject::List(items.iter().map(json_to_monty).collect()),
        JsonValue::Object(map) => {
            let pairs: Vec<(MontyObject, MontyObject)> = map
                .iter()
                .map(|(k, v)| (MontyObject::String(k.clone()), json_to_monty(v)))
                .collect();
            MontyObject::Dict(pairs.into())
        }
    }
}

fn monty_args_to_json(args: &[MontyObject]) -> JsonValue {
    JsonValue::Array(args.iter().map(monty_to_json).collect())
}

fn monty_kwargs_to_json(kwargs: &[(MontyObject, MontyObject)]) -> JsonValue {
    let mut map = serde_json::Map::new();
    for (k, v) in kwargs {
        let key = match k {
            MontyObject::String(s) => s.clone(),
            other => format!("{other:?}"),
        };
        map.insert(key, monty_to_json(v));
    }
    JsonValue::Object(map)
}

/// Merge positional args and kwargs into a single JSON object.
/// Positional args are mapped to parameter names by index.
fn merge_args(
    func_name: &str,
    args: &[MontyObject],
    kwargs: &[(MontyObject, MontyObject)],
    param_registry: &HashMap<String, Vec<String>>,
) -> JsonValue {
    let mut map = serde_json::Map::new();
    // Map positional args to parameter names.
    if let Some(param_names) = param_registry.get(func_name) {
        for (i, arg) in args.iter().enumerate() {
            if i < param_names.len() {
                map.insert(param_names[i].clone(), monty_to_json(arg));
            }
        }
    }
    // Merge kwargs (overrides positional — Python semantics).
    for (k, v) in kwargs {
        if let MontyObject::String(key) = k {
            map.insert(key.clone(), monty_to_json(v));
        }
    }
    JsonValue::Object(map)
}

/// External function definition with parameter names.
#[derive(Deserialize, Clone)]
struct ExtFuncDef {
    name: String,
    #[serde(default)]
    params: Vec<String>,
}

// ---------------------------------------------------------------------------
// Global state
// ---------------------------------------------------------------------------

static STATE: Mutex<Option<State>> = Mutex::new(None);
static RESULT_BUF: Mutex<Vec<u8>> = Mutex::new(Vec::new());
/// Parameter names per host function, used to name positional arguments.
static PARAM_REGISTRY: Mutex<Option<HashMap<String, Vec<String>>>> = Mutex::new(None);

/// A compiled script plus the layout of the values its inputs expect.
struct RunnerState {
    runner: MontyRun,
    /// Some(name) marks a slot filled with a host Function object for that name;
    /// None marks a slot the caller supplies a value for.
    slots: Vec<Option<String>>,
}

/// Execution suspended at a host interaction point.
///
/// Monty hands each suspension back as its own type now (the internal snapshot
/// type is private), so the shim stores whichever variant is live.
enum Suspension {
    Call(FunctionCall),
    Os(OsCall),
    Futures(ResolveFutures),
}

struct State {
    next_id: u32,
    runners: HashMap<u32, RunnerState>,
    suspensions: HashMap<u32, Suspension>,
    /// Host interactions serviced during the current script execution.
    serviced: usize,
    /// Ceiling on them. Monty stores max_suspensions but leaves enforcement to
    /// the host, so the shim counts and aborts.
    max_suspensions: usize,
}

impl State {
    fn new() -> Self {
        Self {
            next_id: 1,
            runners: HashMap::new(),
            suspensions: HashMap::new(),
            serviced: 0,
            max_suspensions: monty_types::DEFAULT_MAX_SUSPENSIONS,
        }
    }

    fn next_handle(&mut self) -> u32 {
        let id = self.next_id;
        self.next_id += 1;
        id
    }
}

fn with_state<F, R>(f: F) -> R
where
    F: FnOnce(&mut State) -> R,
{
    let mut guard = STATE.lock().unwrap();
    let state = guard.get_or_insert_with(State::new);
    f(state)
}

fn with_param_registry<F, R>(f: F) -> R
where
    F: FnOnce(&HashMap<String, Vec<String>>) -> R,
{
    let guard = PARAM_REGISTRY.lock().unwrap();
    static EMPTY: std::sync::LazyLock<HashMap<String, Vec<String>>> =
        std::sync::LazyLock::new(HashMap::new);
    let registry = guard.as_ref().unwrap_or(&EMPTY);
    f(registry)
}

/// Counts one serviced suspension, returning the ceiling if it is exhausted.
fn note_suspension() -> Result<(), usize> {
    with_state(|s| {
        s.serviced += 1;
        if s.serviced > s.max_suspensions {
            Err(s.max_suspensions)
        } else {
            Ok(())
        }
    })
}

// ---------------------------------------------------------------------------
// Result buffer helpers
// ---------------------------------------------------------------------------

fn set_result(data: &[u8]) {
    let mut buf = RESULT_BUF.lock().unwrap();
    buf.clear();
    buf.extend_from_slice(data);
}

fn set_result_json<T: Serialize>(value: &T) {
    let json = serde_json::to_vec(value).unwrap_or_default();
    set_result(&json);
}

// ---------------------------------------------------------------------------
// JSON wire types
// ---------------------------------------------------------------------------

/// Status codes returned to the Go side.
const STATUS_ERROR: u32 = 0;
const STATUS_COMPLETE: u32 = 1;
const STATUS_FUNCTION_CALL: u32 = 2;
const STATUS_OS_CALL: u32 = 3;
const STATUS_RESOLVE_FUTURES: u32 = 4;

#[derive(Serialize)]
struct ProgressResult {
    status: &'static str,
    #[serde(skip_serializing_if = "Option::is_none")]
    value: Option<JsonValue>,
    #[serde(skip_serializing_if = "Option::is_none")]
    snapshot_handle: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    function_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    os_function: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    args: Option<JsonValue>,
    #[serde(skip_serializing_if = "Option::is_none")]
    kwargs: Option<JsonValue>,
    #[serde(skip_serializing_if = "Option::is_none")]
    call_id: Option<u32>,
    /// Set when the call is routed to a host-backed object rather than a plain
    /// external function; the receiver is not in args.
    #[serde(skip_serializing_if = "Option::is_none")]
    object_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pending_call_ids: Option<Vec<u32>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    print_output: Option<String>,
}

impl ProgressResult {
    fn new(status: &'static str, print: &str) -> Self {
        Self {
            status,
            value: None,
            snapshot_handle: None,
            function_name: None,
            os_function: None,
            args: None,
            kwargs: None,
            call_id: None,
            object_id: None,
            pending_call_ids: None,
            error: None,
            print_output: if print.is_empty() {
                None
            } else {
                Some(print.to_owned())
            },
        }
    }
}

fn status_code(status: &str) -> u32 {
    match status {
        "complete" => STATUS_COMPLETE,
        "function_call" => STATUS_FUNCTION_CALL,
        "os_call" => STATUS_OS_CALL,
        "resolve_futures" => STATUS_RESOLVE_FUTURES,
        _ => STATUS_ERROR,
    }
}

#[derive(Deserialize, Default)]
struct LimitsInput {
    #[serde(default)]
    max_duration_ms: Option<u64>,
    #[serde(default)]
    max_memory: Option<u64>,
    #[serde(default)]
    max_recursion_depth: Option<usize>,
    #[serde(default)]
    max_suspensions: Option<usize>,
}

impl LimitsInput {
    fn into_limits(self) -> ResourceLimits {
        let mut limits = ResourceLimits::default();
        if let Some(ms) = self.max_duration_ms {
            limits.max_duration = Some(Duration::from_millis(ms));
        }
        // usize is 32-bit on wasm32, so a limit that cannot be expressed is
        // dropped rather than truncated — matching upstream's note that a cap
        // near 4 GiB saturates and leaves the module uncapped.
        limits.max_memory = self.max_memory.and_then(|b| usize::try_from(b).ok());
        if let Some(depth) = self.max_recursion_depth {
            limits.max_recursion_depth = depth;
        }
        if let Some(susp) = self.max_suspensions {
            limits.max_suspensions = susp;
        }
        limits
    }
}

// ---------------------------------------------------------------------------
// Progress handling
// ---------------------------------------------------------------------------

fn print_to(buf: &mut String) -> PrintWriter<'_> {
    PrintWriter::CollectString(buf, None)
}

fn error_result(err: &MontyException, print: &str) -> ProgressResult {
    str_error_result(&format!("{err}"), print)
}

fn str_error_result(msg: &str, print: &str) -> ProgressResult {
    let mut result = ProgressResult::new("error", print);
    result.error = Some(msg.to_owned());
    result
}

fn complete_result(value: MontyObject, print: &str) -> ProgressResult {
    let mut result = ProgressResult::new("complete", print);
    result.value = Some(monty_to_json(&value));
    result
}

/// Answers a name lookup on the host's behalf.
///
/// Names the caller declared as host functions resolve to a callable
/// (MontyObject::Function) — passing them as inputs already makes them globals,
/// so this is the fallback path. Everything else is deliberately left undefined
/// so the sandbox raises NameError/AttributeError for it.
fn resolve_name(name: &str) -> NameLookupResult {
    let known = with_param_registry(|registry| registry.contains_key(name));
    if known {
        NameLookupResult::Value(MontyObject::Function {
            name: name.to_owned(),
            docstring: None,
        })
    } else {
        NameLookupResult::Undefined
    }
}

/// Aborts a suspension with an uncatchable exception and reports the outcome.
fn abort_progress(progress: RunProgress, print_buf: &mut String, message: &str) -> ProgressResult {
    let exc = MontyException::new(ExcType::RuntimeError, Some(message.to_owned()));
    let outcome = match progress {
        RunProgress::FunctionCall(call) => call.abort(exc, print_to(print_buf)),
        RunProgress::OsCall(call) => call.abort(exc, print_to(print_buf)),
        RunProgress::NameLookup(lookup) => lookup.abort(exc, print_to(print_buf)),
        RunProgress::ResolveFutures(futures) => futures.abort(exc, print_to(print_buf)),
        RunProgress::Complete(value) => return complete_result(value, print_buf),
    };
    match outcome {
        Ok(_) => str_error_result("run aborted without an exception", print_buf),
        Err(err) => error_result(&err, print_buf),
    }
}

/// Turns one step of the interpreter into the wire result the Go side reads.
///
/// Name lookups are resolved here rather than surfaced: the Go API has no
/// name-lookup callback, and answering them in-process keeps the host loop
/// (execute -> resume) unchanged.
fn handle_progress(progress: RunProgress, print_buf: &mut String) -> ProgressResult {
    let mut current = progress;

    loop {
        // The suspension ceiling covers every host interaction, including the
        // ones the shim answers itself.
        if let Err(max) = note_suspension() {
            let message = format!("maximum number of host suspensions ({max}) exceeded");
            return abort_progress(current, print_buf, &message);
        }

        match current {
            RunProgress::Complete(value) => return complete_result(value, print_buf),

            RunProgress::FunctionCall(call) => {
                let merged = with_param_registry(|registry| {
                    merge_args(&call.function_name, &call.args, &call.kwargs, registry)
                });
                let function_name = call.function_name.clone();
                let call_id = call.call_id;
                let object_id = call.object_id.map(|id| id.to_string());
                let handle = with_state(|s| {
                    let handle = s.next_handle();
                    s.suspensions.insert(handle, Suspension::Call(call));
                    handle
                });
                let mut result = ProgressResult::new("function_call", print_buf);
                result.snapshot_handle = Some(handle);
                result.function_name = Some(function_name);
                result.args = Some(merged);
                result.call_id = Some(call_id);
                result.object_id = object_id;
                return result;
            }

            RunProgress::OsCall(call) => {
                let name = call.function_call.name().to_owned();
                let (args, kwargs) = call.function_call.clone().to_args();
                let call_id = call.call_id;
                let handle = with_state(|s| {
                    let handle = s.next_handle();
                    s.suspensions.insert(handle, Suspension::Os(call));
                    handle
                });
                let mut result = ProgressResult::new("os_call", print_buf);
                result.snapshot_handle = Some(handle);
                result.os_function = Some(name);
                result.args = Some(monty_args_to_json(&args));
                result.kwargs = Some(monty_kwargs_to_json(&kwargs));
                result.call_id = Some(call_id);
                return result;
            }

            RunProgress::ResolveFutures(futures) => {
                let pending = futures.pending_call_ids().to_vec();
                let handle = with_state(|s| {
                    let handle = s.next_handle();
                    s.suspensions.insert(handle, Suspension::Futures(futures));
                    handle
                });
                let mut result = ProgressResult::new("resolve_futures", print_buf);
                result.snapshot_handle = Some(handle);
                result.pending_call_ids = Some(pending);
                return result;
            }

            RunProgress::NameLookup(lookup) => {
                let answer = resolve_name(&lookup.name);
                match lookup.resume(answer, print_to(print_buf)) {
                    Ok(next) => current = next,
                    Err(err) => return error_result(&err, print_buf),
                }
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Memory management exports
// ---------------------------------------------------------------------------

#[no_mangle]
pub extern "C" fn wasm_alloc(size: u32) -> u32 {
    if size == 0 {
        return 0;
    }
    let layout = Layout::from_size_align(size as usize, 1).unwrap();
    let ptr = unsafe { alloc(layout) };
    if ptr.is_null() {
        return 0;
    }
    ptr as u32
}

#[no_mangle]
pub extern "C" fn wasm_dealloc(ptr: u32, size: u32) {
    if ptr == 0 || size == 0 {
        return;
    }
    let layout = Layout::from_size_align(size as usize, 1).unwrap();
    unsafe {
        dealloc(ptr as *mut u8, layout);
    }
}

// ---------------------------------------------------------------------------
// Result buffer exports
// ---------------------------------------------------------------------------

#[no_mangle]
pub extern "C" fn monty_result_len() -> u32 {
    RESULT_BUF.lock().unwrap().len() as u32
}

#[no_mangle]
pub extern "C" fn monty_result_read(buf_ptr: u32, buf_cap: u32) -> u32 {
    let result = RESULT_BUF.lock().unwrap();
    let len = result.len().min(buf_cap as usize);
    unsafe {
        std::ptr::copy_nonoverlapping(result.as_ptr(), buf_ptr as *mut u8, len);
    }
    len as u32
}

// ---------------------------------------------------------------------------
// Core API exports
// ---------------------------------------------------------------------------

/// Read a UTF-8 string from WASM linear memory.
unsafe fn read_str(ptr: u32, len: u32) -> String {
    if ptr == 0 || len == 0 {
        return String::new();
    }
    let slice = unsafe { std::slice::from_raw_parts(ptr as *const u8, len as usize) };
    String::from_utf8_lossy(slice).into_owned()
}

/// Parse a JSON array of plain values into Vec<MontyObject>.
fn parse_inputs(json_str: &str) -> Vec<MontyObject> {
    if json_str.is_empty() {
        return vec![];
    }
    let values: Vec<JsonValue> = serde_json::from_str(json_str).unwrap_or_default();
    values.iter().map(json_to_monty).collect()
}

/// Parse a JSON value (return value from Go) into MontyObject.
fn parse_return_value(json_str: &str) -> MontyObject {
    if json_str.is_empty() {
        return MontyObject::None;
    }
    let value: JsonValue = serde_json::from_str(json_str).unwrap_or(JsonValue::Null);
    json_to_monty(&value)
}

/// Compile Python code. Returns a runner handle (>0) on success, 0 on error.
///
/// input_names_json holds the caller's input names in the order their values
/// will arrive at monty_start. Host function names are appended to that list,
/// and their slots are filled by the shim with MontyObject::Function — so a name
/// declared both as an input and as a host function stays an input.
#[no_mangle]
pub extern "C" fn monty_compile(
    code_ptr: u32,
    code_len: u32,
    input_names_ptr: u32,
    input_names_len: u32,
    ext_funcs_ptr: u32,
    ext_funcs_len: u32,
) -> u32 {
    let code = unsafe { read_str(code_ptr, code_len) };
    let input_names_json = unsafe { read_str(input_names_ptr, input_names_len) };
    let ext_funcs_json = unsafe { read_str(ext_funcs_ptr, ext_funcs_len) };

    let input_names: Vec<String> = if input_names_json.is_empty() {
        vec![]
    } else {
        serde_json::from_str(&input_names_json).unwrap_or_default()
    };

    let ext_func_defs: Vec<ExtFuncDef> = if ext_funcs_json.is_empty() {
        vec![]
    } else {
        serde_json::from_str(&ext_funcs_json).unwrap_or_default()
    };

    // Record parameter names for positional-argument mapping and, at the same
    // time, the set of names that resolve to host functions.
    {
        let mut registry_guard = PARAM_REGISTRY.lock().unwrap();
        let registry = registry_guard.get_or_insert_with(HashMap::new);
        registry.clear();
        for def in &ext_func_defs {
            registry.insert(def.name.clone(), def.params.clone());
        }
    }

    // Compose the full input list: caller inputs first (their values arrive in
    // this order), then any host function not already shadowed by an input.
    let mut names = input_names;
    let mut slots: Vec<Option<String>> = vec![None; names.len()];
    for def in &ext_func_defs {
        if names.iter().any(|existing| existing == &def.name) {
            continue;
        }
        names.push(def.name.clone());
        slots.push(Some(def.name.clone()));
    }

    match MontyRun::new(code, "script.py", names, CompileOptions::default()) {
        Ok(runner) => with_state(|s| {
            let handle = s.next_handle();
            s.runners.insert(handle, RunnerState { runner, slots });
            handle
        }),
        Err(e) => {
            set_result_json(&error_result(&e, ""));
            0
        }
    }
}

/// Start execution. Returns a status code:
///   1 = complete, 2 = function_call, 3 = os_call, 4 = resolve_futures, 0 = error
#[no_mangle]
pub extern "C" fn monty_start(
    runner_handle: u32,
    inputs_ptr: u32,
    inputs_len: u32,
    limits_ptr: u32,
    limits_len: u32,
) -> u32 {
    let state = with_state(|s| s.runners.remove(&runner_handle));
    let RunnerState { runner, slots } = match state {
        Some(runner) => runner,
        None => {
            set_result_json(&str_error_result("invalid runner handle", ""));
            return STATUS_ERROR;
        }
    };

    // Parse inputs as plain JSON -> MontyObject.
    let inputs_json = unsafe { read_str(inputs_ptr, inputs_len) };
    let caller_inputs = parse_inputs(&inputs_json);

    // Parse limits.
    let limits_json = unsafe { read_str(limits_ptr, limits_len) };
    let limits_input: LimitsInput = if limits_json.is_empty() {
        LimitsInput::default()
    } else {
        serde_json::from_str(&limits_json).unwrap_or_default()
    };
    let limits = limits_input.into_limits();

    // Arm the allocator's memory ceiling before any sandboxed code runs;
    // set_limit reports whether monty-alloc is actually installed.
    if let Err(err) = monty_alloc::set_limit(limits.max_memory, false) {
        set_result_json(&str_error_result(err, ""));
        return STATUS_ERROR;
    }

    with_state(|s| {
        s.serviced = 0;
        s.max_suspensions = limits.max_suspensions;
    });

    // Interleave caller values with the injected host functions.
    let mut values = Vec::with_capacity(slots.len());
    for (index, slot) in slots.iter().enumerate() {
        match slot {
            Some(name) => values.push(MontyObject::Function {
                name: name.clone(),
                docstring: None,
            }),
            None => match caller_inputs.get(index) {
                Some(value) => values.push(value.clone()),
                None => {
                    set_result_json(&str_error_result(
                        &format!("missing value for input {index}"),
                        "",
                    ));
                    return STATUS_ERROR;
                }
            },
        }
    }

    let tracker = ResourceTracker::new(limits);
    let mut print_buf = String::new();

    match runner.start(values, tracker, print_to(&mut print_buf)) {
        Ok(progress) => {
            let result = handle_progress(progress, &mut print_buf);
            let status = status_code(result.status);
            set_result_json(&result);
            status
        }
        Err(e) => {
            set_result_json(&error_result(&e, &print_buf));
            STATUS_ERROR
        }
    }
}

/// Resume execution after a function call or OS call.
#[no_mangle]
pub extern "C" fn monty_resume(
    snapshot_handle: u32,
    return_value_ptr: u32,
    return_value_len: u32,
) -> u32 {
    let suspension = with_state(|s| s.suspensions.remove(&snapshot_handle));

    let return_json = unsafe { read_str(return_value_ptr, return_value_len) };
    let return_value = parse_return_value(&return_json);
    let mut print_buf = String::new();

    let outcome = match suspension {
        Some(Suspension::Call(call)) => {
            call.resume(ExtFunctionResult::Return(return_value), print_to(&mut print_buf))
        }
        Some(Suspension::Os(call)) => {
            call.resume(ExtFunctionResult::Return(return_value), print_to(&mut print_buf))
        }
        _ => {
            set_result_json(&str_error_result("invalid snapshot handle", ""));
            return STATUS_ERROR;
        }
    };

    finish(outcome, &mut print_buf)
}

/// Resume execution after resolving futures.
#[no_mangle]
pub extern "C" fn monty_resume_futures(
    snapshot_handle: u32,
    results_ptr: u32,
    results_len: u32,
) -> u32 {
    let suspension = with_state(|s| s.suspensions.remove(&snapshot_handle));

    // Parse results as array of [call_id, plain_json_value] pairs.
    let results_json = unsafe { read_str(results_ptr, results_len) };
    let pairs: Vec<(u32, JsonValue)> = if results_json.is_empty() {
        vec![]
    } else {
        serde_json::from_str(&results_json).unwrap_or_default()
    };

    let results: Vec<(u32, ExtFunctionResult)> = pairs
        .into_iter()
        .map(|(id, val)| (id, ExtFunctionResult::Return(json_to_monty(&val))))
        .collect();

    let mut print_buf = String::new();

    let outcome = match suspension {
        Some(Suspension::Futures(futures)) => futures.resume(results, print_to(&mut print_buf)),
        _ => {
            set_result_json(&str_error_result("invalid future snapshot handle", ""));
            return STATUS_ERROR;
        }
    };

    finish(outcome, &mut print_buf)
}

/// Shared tail for the resume paths: turn a step outcome into a wire result.
fn finish(outcome: Result<RunProgress, MontyException>, print_buf: &mut String) -> u32 {
    match outcome {
        Ok(progress) => {
            let result = handle_progress(progress, print_buf);
            let status = status_code(result.status);
            set_result_json(&result);
            status
        }
        Err(e) => {
            set_result_json(&error_result(&e, print_buf));
            STATUS_ERROR
        }
    }
}

/// Free a runner handle.
#[no_mangle]
pub extern "C" fn monty_free_runner(handle: u32) {
    with_state(|s| {
        s.runners.remove(&handle);
    });
}

/// Free a snapshot handle.
#[no_mangle]
pub extern "C" fn monty_free_snapshot(handle: u32) {
    with_state(|s| {
        s.suspensions.remove(&handle);
    });
}
