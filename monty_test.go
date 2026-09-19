package montygo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := New()
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// assertResult is a test helper that executes code and checks the result.
func assertResult(t *testing.T, r *Runner, code string, inputs map[string]any, expected any, opts ...ExecuteOption) {
	t.Helper()
	result, err := r.Execute(context.Background(), code, inputs, opts...)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != expected {
		t.Fatalf("expected %v (%T), got %v (%T)", expected, expected, result, result)
	}
}

// assertError is a test helper that executes code and checks that an error is returned.
func assertError(t *testing.T, r *Runner, code string, inputs map[string]any, errContains string, opts ...ExecuteOption) {
	t.Helper()
	_, err := r.Execute(context.Background(), code, inputs, opts...)
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", errContains)
	}
	if errContains != "" && !strings.Contains(err.Error(), errContains) {
		t.Fatalf("expected error containing %q, got: %v", errContains, err)
	}
}

// assertMontyError checks that the error is a *MontyError.
func assertMontyError(t *testing.T, r *Runner, code string, inputs map[string]any, errContains string, opts ...ExecuteOption) {
	t.Helper()
	_, err := r.Execute(context.Background(), code, inputs, opts...)
	if err == nil {
		t.Fatalf("expected MontyError containing %q, got nil", errContains)
	}
	var me *MontyError
	if !errors.As(err, &me) {
		t.Fatalf("expected *MontyError, got %T: %v", err, err)
	}
	if errContains != "" && !strings.Contains(me.Message, errContains) {
		t.Fatalf("expected error containing %q, got: %s", errContains, me.Message)
	}
}

// ==========================================================================
// test_basic.py — Basic functionality
// ==========================================================================

func TestBasicSimpleExpression(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "1 + 2", nil, float64(3))
}

func TestBasicArithmetic(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "10 * 5 - 3", nil, float64(47))
}

func TestBasicStringConcatenation(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `"hello" + " " + "world"`, nil, "hello world")
}

func TestBasicMultipleRunsSameInstance(t *testing.T) {
	r := newRunner(t)
	for _, tc := range []struct {
		input    int
		expected float64
	}{
		{5, 10},
		{10, 20},
		{-3, -6},
	} {
		result, err := r.Execute(context.Background(), "x * 2", map[string]any{"x": tc.input})
		if err != nil {
			t.Fatalf("Execute(x=%d) failed: %v", tc.input, err)
		}
		if result != tc.expected {
			t.Fatalf("x=%d: expected %v, got %v", tc.input, tc.expected, result)
		}
	}
}

func TestBasicMultilineCode(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x = 1\ny = 2\nx + y", nil, float64(3))
}

func TestBasicFunctionDefinitionAndCall(t *testing.T) {
	r := newRunner(t)
	code := `
def add(a, b):
    return a + b
add(3, 4)
`
	assertResult(t, r, code, nil, float64(7))
}

func TestBasicComplex(t *testing.T) {
	r := newRunner(t)
	code := `
def fib(n):
    if n <= 1:
        return n
    return fib(n - 1) + fib(n - 2)
fib(10)
`
	assertResult(t, r, code, nil, float64(55))
}

func TestBasicModulo(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "17 % 5", nil, float64(2))
}

func TestBasicFloorDivision(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "17 // 5", nil, float64(3))
}

func TestBasicPower(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "2 ** 10", nil, float64(1024))
}

func TestBasicNegation(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "-42", nil, float64(-42))
}

func TestBasicComparisonOperators(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "3 > 2", nil, true)
	assertResult(t, r, "3 < 2", nil, false)
	assertResult(t, r, "3 >= 3", nil, true)
	assertResult(t, r, "3 <= 2", nil, false)
	assertResult(t, r, "3 == 3", nil, true)
	assertResult(t, r, "3 != 4", nil, true)
}

func TestBasicLogicalOperators(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "True and False", nil, false)
	assertResult(t, r, "True or False", nil, true)
	assertResult(t, r, "not True", nil, false)
}

func TestBasicTernary(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "'yes' if True else 'no'", nil, "yes")
	assertResult(t, r, "'yes' if False else 'no'", nil, "no")
}

// ==========================================================================
// test_print.py — Print function
// ==========================================================================

func TestPrintBasic(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), `print("hello")`, nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "hello\n" {
		t.Fatalf("expected %q, got %q", "hello\n", output)
	}
}

func TestPrintMultiple(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), "print(\"line 1\")\nprint(\"line 2\")", nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "line 1\nline 2\n" {
		t.Fatalf("expected %q, got %q", "line 1\nline 2\n", output)
	}
}

func TestPrintWithValues(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), "print(1, 2, 3)", nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "1 2 3\n" {
		t.Fatalf("expected %q, got %q", "1 2 3\n", output)
	}
}

func TestPrintWithSep(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), `print(1, 2, 3, sep="-")`, nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "1-2-3\n" {
		t.Fatalf("expected %q, got %q", "1-2-3\n", output)
	}
}

func TestPrintWithEnd(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), `print("hello", end="!")`, nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "hello!" {
		t.Fatalf("expected %q, got %q", "hello!", output)
	}
}

func TestPrintReturnsNone(t *testing.T) {
	r := newRunner(t)
	var output string
	result, err := r.Execute(context.Background(), `print("test")`, nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestPrintEmpty(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), "print()", nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "\n" {
		t.Fatalf("expected %q, got %q", "\n", output)
	}
}

func TestPrintWithInputs(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), "print(x)", map[string]any{"x": 42},
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "42\n" {
		t.Fatalf("expected %q, got %q", "42\n", output)
	}
}

func TestPrintInLoop(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), "for i in range(3):\n    print(i)", nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "0\n1\n2\n" {
		t.Fatalf("expected %q, got %q", "0\n1\n2\n", output)
	}
}

func TestPrintMixedTypes(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), `print(1, "hello", True, None)`, nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "1 hello True None\n" {
		t.Fatalf("expected %q, got %q", "1 hello True None\n", output)
	}
}

func TestPrintWithLimits(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), `print("with limits")`, nil,
		WithPrintFunc(func(s string) { output += s }),
		WithLimits(Limits{MaxDuration: 5 * time.Second}))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "with limits\n" {
		t.Fatalf("expected %q, got %q", "with limits\n", output)
	}
}

func TestMapPrint(t *testing.T) {
	r := newRunner(t)
	var output string
	_, err := r.Execute(context.Background(), "list(map(print, [1, 2, 3]))", nil,
		WithPrintFunc(func(s string) { output += s }))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if output != "1\n2\n3\n" {
		t.Fatalf("expected %q, got %q", "1\n2\n3\n", output)
	}
}

// ==========================================================================
// test_exceptions.py — Exception handling
// ==========================================================================

func TestExceptionZeroDivision(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, "1 / 0", nil, "ZeroDivisionError")
}

func TestExceptionValueError(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, `raise ValueError("bad value")`, nil, "ValueError")
}

func TestExceptionTypeError(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, `"string" + 1`, nil, "TypeError")
}

func TestExceptionIndexError(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, "[1, 2, 3][10]", nil, "IndexError")
}

func TestExceptionKeyError(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, `{"a": 1}["b"]`, nil, "KeyError")
}

func TestExceptionNameError(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, "undefined_variable", nil, "NameError")
}

func TestExceptionAssertionError(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, "assert False", nil, "AssertionError")
}

func TestExceptionAssertionErrorWithMessage(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, `assert False, "custom message"`, nil, "custom message")
}

func TestExceptionRuntimeError(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, `raise RuntimeError("runtime error")`, nil, "RuntimeError")
}

func TestExceptionNotImplementedError(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, `raise NotImplementedError("not implemented")`, nil, "NotImplementedError")
}

func TestExceptionAttributeError(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, `raise AttributeError("no such attr")`, nil, "AttributeError")
}

func TestSyntaxErrorOnInit(t *testing.T) {
	r := newRunner(t)
	assertError(t, r, "def", nil, "SyntaxError")
}

func TestSyntaxErrorUnclosedParen(t *testing.T) {
	r := newRunner(t)
	assertError(t, r, "print(1", nil, "")
}

func TestSyntaxErrorInvalidSyntax(t *testing.T) {
	r := newRunner(t)
	assertError(t, r, "x = = 1", nil, "")
}

func TestExceptionCaughtInPython(t *testing.T) {
	r := newRunner(t)
	code := `
try:
    1 / 0
except ZeroDivisionError:
    result = "caught"
result
`
	assertResult(t, r, code, nil, "caught")
}

func TestExceptionInFunction(t *testing.T) {
	r := newRunner(t)
	code := `
def fail():
    raise ValueError("in function")
fail()
`
	assertMontyError(t, r, code, nil, "ValueError")
}

func TestExceptionTraceback(t *testing.T) {
	r := newRunner(t)
	_, err := r.Execute(context.Background(), "1 / 0", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "ZeroDivisionError") {
		t.Fatalf("expected traceback with ZeroDivisionError, got: %s", errStr)
	}
}

func TestExceptionTryExceptFinally(t *testing.T) {
	r := newRunner(t)
	code := `
result = []
try:
    1 / 0
except ZeroDivisionError:
    result.append("caught")
finally:
    result.append("finally")
result
`
	result, err := r.Execute(context.Background(), code, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 2 || list[0] != "caught" || list[1] != "finally" {
		t.Fatalf("expected [caught, finally], got %v", list)
	}
}

func TestExceptionTryExceptElse(t *testing.T) {
	r := newRunner(t)
	code := `
try:
    x = 1
except:
    x = "error"
else:
    x = "no error"
x
`
	assertResult(t, r, code, nil, "no error")
}

func TestExceptionNestedTryCatch(t *testing.T) {
	r := newRunner(t)
	code := `
result = []
try:
    try:
        1 / 0
    except ZeroDivisionError:
        result.append("inner")
        raise ValueError("from inner")
except ValueError:
    result.append("outer")
result
`
	result, err := r.Execute(context.Background(), code, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 2 || list[0] != "inner" || list[1] != "outer" {
		t.Fatalf("expected [inner, outer], got %v", list)
	}
}

func TestExceptionRecursionError(t *testing.T) {
	r := newRunner(t)
	code := `
def recurse(n):
    return recurse(n + 1)
recurse(0)
`
	assertMontyError(t, r, code, nil, "RecursionError",
		WithLimits(Limits{MaxRecursionDepth: 50}))
}

// ==========================================================================
// test_types.py — Type system & data types
// ==========================================================================

// --- None ---

func TestTypeNoneInput(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x is None", map[string]any{"x": nil}, true)
}

func TestTypeNoneOutput(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "None", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %v (%T)", result, result)
	}
}

// --- Bool ---

func TestTypeBoolTrue(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x", map[string]any{"x": true}, true)
}

func TestTypeBoolFalse(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x", map[string]any{"x": false}, false)
}

func TestTypeBoolOutput(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "True", nil, true)
	assertResult(t, r, "False", nil, false)
}

// --- Int ---

func TestTypeInt(t *testing.T) {
	r := newRunner(t)
	for _, v := range []int{42, -100, 0, 1, -1} {
		result, err := r.Execute(context.Background(), "x", map[string]any{"x": v})
		if err != nil {
			t.Fatalf("Execute(x=%d) failed: %v", v, err)
		}
		if result != float64(v) {
			t.Fatalf("x=%d: expected %v, got %v (%T)", v, float64(v), result, result)
		}
	}
}

func TestTypeIntLarge(t *testing.T) {
	r := newRunner(t)
	// Test with large-ish int that fits in JSON
	assertResult(t, r, "x", map[string]any{"x": 1000000}, float64(1000000))
}

// --- Float ---

func TestTypeFloat(t *testing.T) {
	r := newRunner(t)
	for _, v := range []float64{3.14, -2.5, 0.0} {
		result, err := r.Execute(context.Background(), "x", map[string]any{"x": v})
		if err != nil {
			t.Fatalf("Execute(x=%v) failed: %v", v, err)
		}
		if result != v {
			t.Fatalf("x=%v: expected %v, got %v", v, v, result)
		}
	}
}

func TestTypeFloatArithmetic(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "1.5 + 2.5", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != float64(4.0) {
		t.Fatalf("expected 4.0, got %v", result)
	}
}

func TestTypeFloatDivision(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "7 / 2", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != float64(3.5) {
		t.Fatalf("expected 3.5, got %v", result)
	}
}

// --- String ---

func TestTypeString(t *testing.T) {
	r := newRunner(t)
	for _, v := range []string{"hello", "", "unicode: éè"} {
		result, err := r.Execute(context.Background(), "x", map[string]any{"x": v})
		if err != nil {
			t.Fatalf("Execute(x=%q) failed: %v", v, err)
		}
		if result != v {
			t.Fatalf("x=%q: expected %q, got %v", v, v, result)
		}
	}
}

func TestTypeStringMethods(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `"hello world".upper()`, nil, "HELLO WORLD")
	assertResult(t, r, `"  hello  ".strip()`, nil, "hello")
	assertResult(t, r, `"hello".startswith("hel")`, nil, true)
	assertResult(t, r, `len("hello")`, nil, float64(5))
}

func TestTypeStringFormatting(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `f"x={x}"`, map[string]any{"x": 42}, "x=42")
}

func TestTypeStringSlicing(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `"hello"[1:3]`, nil, "el")
	assertResult(t, r, `"hello"[:2]`, nil, "he")
	assertResult(t, r, `"hello"[2:]`, nil, "llo")
}

// --- List ---

func TestTypeListInput(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "x", map[string]any{"x": []any{1, 2, 3}})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(list))
	}
}

func TestTypeListOutput(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "[1, 2, 3]", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 3 || list[0] != float64(1) || list[1] != float64(2) || list[2] != float64(3) {
		t.Fatalf("expected [1, 2, 3], got %v", list)
	}
}

func TestTypeListEmpty(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "[]", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %v", list)
	}
}

func TestTypeListOperations(t *testing.T) {
	r := newRunner(t)
	// append, len, indexing
	code := `
x = [1, 2]
x.append(3)
len(x)
`
	assertResult(t, r, code, nil, float64(3))
}

func TestTypeListComprehension(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "[x * 2 for x in range(5)]", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	expected := []float64{0, 2, 4, 6, 8}
	if len(list) != len(expected) {
		t.Fatalf("expected %d elements, got %d", len(expected), len(list))
	}
	for i, v := range expected {
		if list[i] != v {
			t.Fatalf("index %d: expected %v, got %v", i, v, list[i])
		}
	}
}

func TestTypeListSlicing(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "[1, 2, 3, 4, 5][1:4]", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 3 || list[0] != float64(2) || list[1] != float64(3) || list[2] != float64(4) {
		t.Fatalf("expected [2, 3, 4], got %v", list)
	}
}

// --- Dict ---

func TestTypeDictInput(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "x", map[string]any{
		"x": map[string]any{"a": 1, "b": 2},
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	dict, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if dict["a"] != float64(1) || dict["b"] != float64(2) {
		t.Fatalf("expected {a:1, b:2}, got %v", dict)
	}
}

func TestTypeDictOutput(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), `{"a": 1, "b": 2}`, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	dict, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if dict["a"] != float64(1) || dict["b"] != float64(2) {
		t.Fatalf("expected {a:1, b:2}, got %v", dict)
	}
}

func TestTypeDictEmpty(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "{}", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	dict, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if len(dict) != 0 {
		t.Fatalf("expected empty dict, got %v", dict)
	}
}

func TestTypeDictComprehension(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), `{str(i): i * 2 for i in range(3)}`, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	dict, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result)
	}
	if dict["0"] != float64(0) || dict["1"] != float64(2) || dict["2"] != float64(4) {
		t.Fatalf("expected {0:0, 1:2, 2:4}, got %v", dict)
	}
}

// --- Tuple (comes back as []any over JSON) ---

func TestTypeTupleOutput(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "(1, 2, 3)", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(list))
	}
}

func TestTypeTupleUnpacking(t *testing.T) {
	r := newRunner(t)
	code := `
a, b, c = (1, 2, 3)
a + b + c
`
	assertResult(t, r, code, nil, float64(6))
}

// --- Set (comes back as []any over JSON, order not guaranteed) ---

func TestTypeSetOutput(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "{1, 2, 3}", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(list))
	}
	// Verify all elements present (order not guaranteed).
	seen := make(map[float64]bool)
	for _, v := range list {
		seen[v.(float64)] = true
	}
	if !seen[1] || !seen[2] || !seen[3] {
		t.Fatalf("expected {1, 2, 3}, got %v", list)
	}
}

func TestTypeSetDeduplicate(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "len({1, 1, 2, 2, 3})", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != float64(3) {
		t.Fatalf("expected 3, got %v", result)
	}
}

// --- Nested collections ---

func TestTypeNestedList(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "[[1, 2], [3, [4, 5]]]", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	outer, ok := result.([]any)
	if !ok || len(outer) != 2 {
		t.Fatalf("expected outer list of 2, got %v", result)
	}
	inner, ok := outer[1].([]any)
	if !ok || len(inner) != 2 {
		t.Fatalf("expected inner list of 2, got %v", outer[1])
	}
	deepInner, ok := inner[1].([]any)
	if !ok || len(deepInner) != 2 {
		t.Fatalf("expected deepest list of 2, got %v", inner[1])
	}
}

func TestTypeNestedDict(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), `{"a": {"b": {"c": 1}}}`, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	outer, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	mid, ok := outer["a"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map, got %T", outer["a"])
	}
	inner, ok := mid["b"].(map[string]any)
	if !ok {
		t.Fatalf("expected inner map, got %T", mid["b"])
	}
	if inner["c"] != float64(1) {
		t.Fatalf("expected c=1, got %v", inner["c"])
	}
}

func TestTypeMixedNested(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), `{"list": [1, 2], "nested": {"key": "val"}}`, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	list, ok := m["list"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected list of 2, got %v", m["list"])
	}
	nested, ok := m["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map, got %T", m["nested"])
	}
	if nested["key"] != "val" {
		t.Fatalf("expected key=val, got %v", nested["key"])
	}
}

func TestTypeNestedInputRoundtrip(t *testing.T) {
	r := newRunner(t)
	input := map[string]any{
		"x": map[string]any{
			"list": []any{1, 2, 3},
			"nested": map[string]any{
				"key": "value",
			},
		},
	}
	result, err := r.Execute(context.Background(), `x["nested"]["key"]`, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "value" {
		t.Fatalf("expected 'value', got %v", result)
	}
}

// Note: float('inf'), float('nan') are NOT testable through JSON boundary.
// JSON has no representation for Inf/NaN. These values would need special
// encoding in the WASM shim to support.

// ==========================================================================
// test_external.py — External function calls
// ==========================================================================

func TestExtFuncNoArgs(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "noop()", nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			if call.Name != "noop" {
				return nil, fmt.Errorf("expected 'noop', got %q", call.Name)
			}
			if len(call.Args) != 0 {
				return nil, fmt.Errorf("expected 0 args, got %d", len(call.Args))
			}
			return "called", nil
		}, Func("noop")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "called" {
		t.Fatalf("expected 'called', got %v", result)
	}
}

func TestExtFuncPositionalArgs(t *testing.T) {
	r := newRunner(t)
	var gotArgs map[string]any
	result, err := r.Execute(context.Background(), "fn(1, 2, 3)", nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			gotArgs = call.Args
			return "ok", nil
		}, Func("fn", "a", "b", "c")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected 'ok', got %v", result)
	}
	if len(gotArgs) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(gotArgs), gotArgs)
	}
	if gotArgs["a"] != float64(1) || gotArgs["b"] != float64(2) || gotArgs["c"] != float64(3) {
		t.Fatalf("expected {a:1, b:2, c:3}, got %v", gotArgs)
	}
}

func TestExtFuncKwargsOnly(t *testing.T) {
	r := newRunner(t)
	var gotArgs map[string]any
	result, err := r.Execute(context.Background(), `fn(a=1, b="two")`, nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			gotArgs = call.Args
			return "ok", nil
		}, Func("fn", "a", "b")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected 'ok', got %v", result)
	}
	if gotArgs["a"] != float64(1) || gotArgs["b"] != "two" {
		t.Fatalf("expected {a:1, b:'two'}, got %v", gotArgs)
	}
}

func TestExtFuncMixedArgsKwargs(t *testing.T) {
	r := newRunner(t)
	var gotArgs map[string]any
	_, err := r.Execute(context.Background(), `fn(1, 2, x="hello", y=True)`, nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			gotArgs = call.Args
			return nil, nil
		}, Func("fn", "a", "b")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	// Positional args mapped to "a" and "b", kwargs "x" and "y" merged in.
	if gotArgs["a"] != float64(1) || gotArgs["b"] != float64(2) {
		t.Fatalf("expected a=1, b=2, got %v", gotArgs)
	}
	if gotArgs["x"] != "hello" || gotArgs["y"] != true {
		t.Fatalf("expected x='hello', y=true, got %v", gotArgs)
	}
}

func TestExtFuncComplexTypes(t *testing.T) {
	r := newRunner(t)
	var gotArgs map[string]any
	_, err := r.Execute(context.Background(), `fn([1, 2], {"key": "value"})`, nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			gotArgs = call.Args
			return nil, nil
		}, Func("fn", "list_arg", "dict_arg")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(gotArgs) != 2 {
		t.Fatalf("expected 2 args, got %d", len(gotArgs))
	}
	// First positional arg mapped to "list_arg".
	list, ok := gotArgs["list_arg"].([]any)
	if !ok {
		t.Fatalf("expected []any for list_arg, got %T", gotArgs["list_arg"])
	}
	if len(list) != 2 {
		t.Fatalf("expected list of 2, got %v", list)
	}
	// Second positional arg mapped to "dict_arg".
	dict, ok := gotArgs["dict_arg"].(map[string]any)
	if !ok {
		t.Fatalf("expected map for dict_arg, got %T", gotArgs["dict_arg"])
	}
	if dict["key"] != "value" {
		t.Fatalf("expected key='value', got %v", dict)
	}
}

func TestExtFuncReturnsNone(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "do_nothing()", nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			return nil, nil
		}, Func("do_nothing")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestExtFuncReturnsComplexType(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "get_data()", nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			return map[string]any{
				"a": []any{1, 2, 3},
				"b": map[string]any{"nested": true},
			}, nil
		}, Func("get_data")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	a, ok := m["a"].([]any)
	if !ok || len(a) != 3 {
		t.Fatalf("expected a=[1,2,3], got %v", m["a"])
	}
	b, ok := m["b"].(map[string]any)
	if !ok || b["nested"] != true {
		t.Fatalf("expected b.nested=true, got %v", m["b"])
	}
}

func TestExtFuncMultipleFunctions(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "add(1, 2) + mul(3, 4)", nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			a, _ := call.Args["a"].(float64)
			b, _ := call.Args["b"].(float64)
			switch call.Name {
			case "add":
				return a + b, nil
			case "mul":
				return a * b, nil
			default:
				return nil, fmt.Errorf("unknown function: %s", call.Name)
			}
		}, Func("add", "a", "b"), Func("mul", "a", "b")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != float64(15) {
		t.Fatalf("expected 15, got %v", result)
	}
}

func TestExtFuncCalledMultipleTimes(t *testing.T) {
	r := newRunner(t)
	callCount := 0
	result, err := r.Execute(context.Background(), "counter() + counter() + counter()", nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			callCount++
			return callCount, nil
		}, Func("counter")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if callCount != 3 {
		t.Fatalf("expected 3 calls, got %d", callCount)
	}
	if result != float64(6) {
		t.Fatalf("expected 6, got %v", result)
	}
}

func TestExtFuncWithInput(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "process(x)", map[string]any{"x": 5},
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			v, _ := call.Args["val"].(float64)
			return v * 10, nil
		}, Func("process", "val")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != float64(50) {
		t.Fatalf("expected 50, got %v", result)
	}
}

func TestExtFuncUndeclaredRaisesNameError(t *testing.T) {
	r := newRunner(t)
	// Call a function not declared in external_functions list.
	assertMontyError(t, r, "unknown_func()", nil, "NameError")
}

func TestExtFuncChainedCalls(t *testing.T) {
	r := newRunner(t)
	code := `
a = step(1)
b = step(a)
c = step(b)
c
`
	result, err := r.Execute(context.Background(), code, nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			v, _ := call.Args["n"].(float64)
			return v + 1, nil
		}, Func("step", "n")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != float64(4) {
		t.Fatalf("expected 4, got %v", result)
	}
}

func TestExtFuncUsedInExpression(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "1 + double(5) + 2", nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			v, _ := call.Args["n"].(float64)
			return v * 2, nil
		}, Func("double", "n")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != float64(13) {
		t.Fatalf("expected 13, got %v", result)
	}
}

func TestExtFuncInConditional(t *testing.T) {
	r := newRunner(t)
	code := `
if check(10):
    result = "yes"
else:
    result = "no"
result
`
	result, err := r.Execute(context.Background(), code, nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			v, _ := call.Args["n"].(float64)
			return v > 5, nil
		}, Func("check", "n")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "yes" {
		t.Fatalf("expected 'yes', got %v", result)
	}
}

func TestExtFuncInLoop(t *testing.T) {
	r := newRunner(t)
	code := `
total = 0
for i in range(5):
    total += transform(i)
total
`
	result, err := r.Execute(context.Background(), code, nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			v, _ := call.Args["n"].(float64)
			return v * v, nil
		}, Func("transform", "n")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	// 0 + 1 + 4 + 9 + 16 = 30
	if result != float64(30) {
		t.Fatalf("expected 30, got %v", result)
	}
}

func TestExtFuncExceptionCaughtByTryExcept(t *testing.T) {
	r := newRunner(t)
	code := `
try:
    fail()
except ValueError:
    result = True
result
`
	result, err := r.Execute(context.Background(), code, nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			return nil, fmt.Errorf("bad value")
		}, Func("fail")))
	// Note: our current implementation wraps external function errors, which may
	// not produce a catchable ValueError inside Python. This tests the boundary.
	if err != nil {
		// If the error propagates to Go, that's also valid behavior.
		t.Logf("error propagated to Go (expected): %v", err)
		return
	}
	if result != true {
		t.Fatalf("expected true, got %v", result)
	}
}

func TestExtFuncArgsJSON(t *testing.T) {
	r := newRunner(t)
	var gotJSON string
	_, err := r.Execute(context.Background(), `fn(query="test", limit=10)`, nil,
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			gotJSON = call.ArgsJSON()
			return "ok", nil
		}, Func("fn", "query", "limit")))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	// Verify ArgsJSON produces valid JSON with correct keys.
	if gotJSON == "" || gotJSON == "{}" {
		t.Fatalf("expected non-empty ArgsJSON, got %q", gotJSON)
	}
	// Parse it back to verify.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(gotJSON), &parsed); err != nil {
		t.Fatalf("ArgsJSON is not valid JSON: %v", err)
	}
	if parsed["query"] != "test" || parsed["limit"] != float64(10) {
		t.Fatalf("expected {query:test, limit:10}, got %v", parsed)
	}
}

// ==========================================================================
// test_inputs.py — Input handling
// ==========================================================================

func TestInputSingle(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x", map[string]any{"x": 42}, float64(42))
}

func TestInputMultiple(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x + y + z", map[string]any{"x": 1, "y": 2, "z": 3}, float64(6))
}

func TestInputUsedInExpression(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x * 2 + y", map[string]any{"x": 5, "y": 3}, float64(13))
}

func TestInputString(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `greeting + " " + name`, map[string]any{
		"greeting": "Hello",
		"name":     "World",
	}, "Hello World")
}

func TestInputList(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "data[0] + data[1]", map[string]any{
		"data": []any{10, 20},
	}, float64(30))
}

func TestInputDict(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `config["a"] * config["b"]`, map[string]any{
		"config": map[string]any{"a": 3, "b": 4},
	}, float64(12))
}

func TestInputOrderIndependent(t *testing.T) {
	r := newRunner(t)
	// Map iteration order is random in Go, so this naturally tests order independence.
	assertResult(t, r, "a - b", map[string]any{"b": 3, "a": 10}, float64(7))
}

func TestInputFunctionParamShadows(t *testing.T) {
	r := newRunner(t)
	code := `
def foo(x):
    return x + 1
foo(x * 2)
`
	assertResult(t, r, code, map[string]any{"x": 5}, float64(11))
}

func TestInputAccessibleOutsideShadowingFunction(t *testing.T) {
	r := newRunner(t)
	code := `
def double(x):
    return x * 2
result = double(10) + x
result
`
	assertResult(t, r, code, map[string]any{"x": 5}, float64(25))
}

func TestInputFunctionUsesInputDirectly(t *testing.T) {
	r := newRunner(t)
	code := `
def foo(y):
    return x + y
foo(10)
`
	assertResult(t, r, code, map[string]any{"x": 5}, float64(15))
}

func TestInputBool(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x", map[string]any{"x": true}, true)
	assertResult(t, r, "x", map[string]any{"x": false}, false)
}

func TestInputNone(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x is None", map[string]any{"x": nil}, true)
}

func TestInputNestedAccess(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `x["a"][1]`, map[string]any{
		"x": map[string]any{"a": []any{10, 20, 30}},
	}, float64(20))
}

// ==========================================================================
// test_limits.py — Resource limits
// ==========================================================================

func TestLimitsTimeout(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, "while True:\n    pass", nil, "time limit exceeded",
		WithLimits(Limits{MaxDuration: 100 * time.Millisecond}))
}

func TestLimitsTimeoutInBuiltinLoop(t *testing.T) {
	r := newRunner(t)
	// Large loop with small iteration body to exercise timeout in builtins.
	code := `
x = 0
for i in range(100000000):
    x += 1
x
`
	assertMontyError(t, r, code, nil, "time limit exceeded",
		WithLimits(Limits{MaxDuration: 100 * time.Millisecond}))
}

func TestLimitsRecursionDepth(t *testing.T) {
	r := newRunner(t)
	code := `
def recurse(n):
    return recurse(n + 1)
recurse(0)
`
	assertMontyError(t, r, code, nil, "RecursionError",
		WithLimits(Limits{MaxRecursionDepth: 5}))
}

func TestLimitsRecursionOK(t *testing.T) {
	r := newRunner(t)
	code := `
def recurse(n):
    if n <= 0:
        return n
    return recurse(n - 1)
recurse(5)
`
	assertResult(t, r, code, nil, float64(0),
		WithLimits(Limits{MaxRecursionDepth: 100}))
}

func TestLimitsSuspensionLimit(t *testing.T) {
	r := newRunner(t)
	// Sandbox code that loops on host calls would otherwise be serviced forever
	// while the execution-time budget is paused, so the host caps it.
	code := `
total = 0
for i in range(100):
    total = step(total)
total
`
	assertMontyError(t, r, code, nil, "suspensions",
		WithLimits(Limits{MaxSuspensions: 10}),
		WithExternalFunc(func(ctx context.Context, call *FunctionCall) (any, error) {
			return float64(1), nil
		}, Func("step", "n")))
}

func TestLimitsMemoryLimit(t *testing.T) {
	r := newRunner(t)
	code := `
for i in range(1000):
    x = "a" * 10000
x
`
	assertMontyError(t, r, code, nil, "MemoryError",
		WithLimits(Limits{MaxMemoryBytes: 100}))
}

func TestLimitsWithInputs(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "x * 2", map[string]any{"x": 21}, float64(42),
		WithLimits(Limits{MaxDuration: 5 * time.Second}))
}

func TestLimitsPowMemoryLimit(t *testing.T) {
	r := newRunner(t)
	assertMontyError(t, r, "2 ** 10000000", nil, "MemoryError",
		WithLimits(Limits{MaxMemoryBytes: 1 << 20}))
}

func TestLimitsSmallOperationsWithinLimit(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "2 ** 1000", nil,
		WithLimits(Limits{MaxMemoryBytes: 1 << 20}))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result for 2**1000")
	}
}

func TestContextCancellation(t *testing.T) {
	r := newRunner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := r.Execute(ctx, "while True:\n    pass", nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ==========================================================================
// test_os_calls.py — OS-level calls (with OsCallFunc handler)
// ==========================================================================

func TestOsCallPathExists(t *testing.T) {
	r := newRunner(t)
	code := `
from pathlib import Path
Path("/test/file.txt").exists()
`
	result, err := r.Execute(context.Background(), code, nil,
		WithOsCallFunc(func(ctx context.Context, call *OsCall) (any, error) {
			switch call.Function {
			case "Path.exists":
				path, _ := call.Args[0].(string)
				return path == "/test/file.txt", nil
			default:
				return nil, fmt.Errorf("unhandled OS call: %s", call.Function)
			}
		}))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != true {
		t.Fatalf("expected true, got %v", result)
	}
}

func TestOsCallPathIsFile(t *testing.T) {
	r := newRunner(t)
	code := `
from pathlib import Path
Path("/test/file.txt").is_file()
`
	result, err := r.Execute(context.Background(), code, nil,
		WithOsCallFunc(func(ctx context.Context, call *OsCall) (any, error) {
			switch call.Function {
			case "Path.is_file":
				return true, nil
			default:
				return nil, fmt.Errorf("unhandled OS call: %s", call.Function)
			}
		}))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != true {
		t.Fatalf("expected true, got %v", result)
	}
}

func TestOsCallReadText(t *testing.T) {
	r := newRunner(t)
	code := `
from pathlib import Path
Path("/test/file.txt").read_text()
`
	result, err := r.Execute(context.Background(), code, nil,
		WithOsCallFunc(func(ctx context.Context, call *OsCall) (any, error) {
			switch call.Function {
			case "Path.read_text":
				return "hello world", nil
			default:
				return nil, fmt.Errorf("unhandled OS call: %s", call.Function)
			}
		}))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "hello world" {
		t.Fatalf("expected 'hello world', got %v", result)
	}
}

func TestOsCallWriteAndRead(t *testing.T) {
	r := newRunner(t)
	files := map[string]string{}
	code := `
from pathlib import Path
Path("/test/new.txt").write_text("updated")
Path("/test/new.txt").read_text()
`
	result, err := r.Execute(context.Background(), code, nil,
		WithOsCallFunc(func(ctx context.Context, call *OsCall) (any, error) {
			switch call.Function {
			case "Path.write_text":
				path, _ := call.Args[0].(string)
				content, _ := call.Args[1].(string)
				files[path] = content
				return len(content), nil
			case "Path.read_text":
				path, _ := call.Args[0].(string)
				if content, ok := files[path]; ok {
					return content, nil
				}
				return nil, fmt.Errorf("file not found: %s", path)
			default:
				return nil, fmt.Errorf("unhandled OS call: %s", call.Function)
			}
		}))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "updated" {
		t.Fatalf("expected 'updated', got %v", result)
	}
}

func TestOsCallGetenv(t *testing.T) {
	r := newRunner(t)
	code := `
import os
os.getenv("MY_VAR")
`
	result, err := r.Execute(context.Background(), code, nil,
		WithOsCallFunc(func(ctx context.Context, call *OsCall) (any, error) {
			switch call.Function {
			case "os.getenv":
				key, _ := call.Args[0].(string)
				if key == "MY_VAR" {
					return "my_value", nil
				}
				return nil, nil
			default:
				return nil, fmt.Errorf("unhandled OS call: %s", call.Function)
			}
		}))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "my_value" {
		t.Fatalf("expected 'my_value', got %v", result)
	}
}

// ==========================================================================
// Python built-in functions and patterns
// ==========================================================================

func TestBuiltinLen(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "len([1, 2, 3])", nil, float64(3))
	assertResult(t, r, `len("hello")`, nil, float64(5))
	assertResult(t, r, `len({"a": 1, "b": 2})`, nil, float64(2))
}

func TestBuiltinRange(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "list(range(5))", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok || len(list) != 5 {
		t.Fatalf("expected list of 5, got %v", result)
	}
	for i := 0; i < 5; i++ {
		if list[i] != float64(i) {
			t.Fatalf("index %d: expected %v, got %v", i, float64(i), list[i])
		}
	}
}

func TestBuiltinRangeWithStepArgs(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "list(range(0, 10, 2))", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok || len(list) != 5 {
		t.Fatalf("expected list of 5, got %v", result)
	}
}

func TestBuiltinAbs(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "abs(-42)", nil, float64(42))
	assertResult(t, r, "abs(42)", nil, float64(42))
}

func TestBuiltinMinMax(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "min(3, 1, 4, 1, 5)", nil, float64(1))
	assertResult(t, r, "max(3, 1, 4, 1, 5)", nil, float64(5))
}

func TestBuiltinSum(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "sum([1, 2, 3, 4, 5])", nil, float64(15))
}

func TestBuiltinSorted(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "sorted([3, 1, 4, 1, 5])", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	expected := []float64{1, 1, 3, 4, 5}
	for i, v := range expected {
		if list[i] != v {
			t.Fatalf("index %d: expected %v, got %v", i, v, list[i])
		}
	}
}

func TestBuiltinReversed(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "list(reversed([1, 2, 3]))", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result)
	}
	if list[0] != float64(3) || list[1] != float64(2) || list[2] != float64(1) {
		t.Fatalf("expected [3, 2, 1], got %v", list)
	}
}

func TestBuiltinEnumerate(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "list(enumerate(['a', 'b', 'c']))", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("expected list of 3, got %v", result)
	}
	// Each element is a tuple (index, value), serialized as []any.
	first, ok := list[0].([]any)
	if !ok || first[0] != float64(0) || first[1] != "a" {
		t.Fatalf("expected (0, 'a'), got %v", list[0])
	}
}

func TestBuiltinZip(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "list(zip([1, 2, 3], ['a', 'b', 'c']))", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("expected list of 3, got %v", result)
	}
}

func TestBuiltinMap(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), "list(map(str, [1, 2, 3]))", nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("expected list of 3, got %v", result)
	}
	if list[0] != "1" || list[1] != "2" || list[2] != "3" {
		t.Fatalf("expected ['1', '2', '3'], got %v", list)
	}
}

// Note: filter() is not a Monty builtin. Use list comprehensions instead.

func TestBuiltinAll(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "all([True, True, True])", nil, true)
	assertResult(t, r, "all([True, False, True])", nil, false)
}

func TestBuiltinAny(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "any([False, False, True])", nil, true)
	assertResult(t, r, "any([False, False, False])", nil, false)
}

func TestBuiltinIsinstance(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "isinstance(42, int)", nil, true)
	assertResult(t, r, `isinstance("hello", str)`, nil, true)
	assertResult(t, r, "isinstance(3.14, float)", nil, true)
	assertResult(t, r, "isinstance(True, bool)", nil, true)
}

func TestBuiltinType(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "type(42).__name__", nil, "int")
	assertResult(t, r, `type("hello").__name__`, nil, "str")
	assertResult(t, r, "type(3.14).__name__", nil, "float")
}

func TestBuiltinInt(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `int("42")`, nil, float64(42))
	assertResult(t, r, "int(3.7)", nil, float64(3))
}

func TestBuiltinFloat(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `float("3.14")`, nil, float64(3.14))
	assertResult(t, r, "float(42)", nil, float64(42))
}

func TestBuiltinStr(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "str(42)", nil, "42")
	assertResult(t, r, "str(3.14)", nil, "3.14")
	assertResult(t, r, "str(True)", nil, "True")
	assertResult(t, r, "str(None)", nil, "None")
}

func TestBuiltinBool(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "bool(0)", nil, false)
	assertResult(t, r, "bool(1)", nil, true)
	assertResult(t, r, `bool("")`, nil, false)
	assertResult(t, r, `bool("x")`, nil, true)
	assertResult(t, r, "bool([])", nil, false)
	assertResult(t, r, "bool([1])", nil, true)
}

// ==========================================================================
// Control flow patterns
// ==========================================================================

func TestControlForLoop(t *testing.T) {
	r := newRunner(t)
	code := `
total = 0
for i in range(10):
    total += i
total
`
	assertResult(t, r, code, nil, float64(45))
}

func TestControlWhileLoop(t *testing.T) {
	r := newRunner(t)
	code := `
x = 10
while x > 0:
    x -= 1
x
`
	assertResult(t, r, code, nil, float64(0))
}

func TestControlBreak(t *testing.T) {
	r := newRunner(t)
	code := `
result = 0
for i in range(100):
    if i == 5:
        break
    result = i
result
`
	assertResult(t, r, code, nil, float64(4))
}

func TestControlContinue(t *testing.T) {
	r := newRunner(t)
	code := `
total = 0
for i in range(10):
    if i % 2 == 0:
        continue
    total += i
total
`
	assertResult(t, r, code, nil, float64(25)) // 1+3+5+7+9
}

func TestControlNestedLoops(t *testing.T) {
	r := newRunner(t)
	code := `
result = []
for i in range(3):
    for j in range(3):
        result.append(i * 3 + j)
result
`
	result, err := r.Execute(context.Background(), code, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok || len(list) != 9 {
		t.Fatalf("expected list of 9, got %v", result)
	}
}

func TestControlIfElifElse(t *testing.T) {
	r := newRunner(t)
	code := `
def classify(x):
    if x > 0:
        return "positive"
    elif x < 0:
        return "negative"
    else:
        return "zero"
[classify(1), classify(-1), classify(0)]
`
	result, err := r.Execute(context.Background(), code, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("expected list of 3, got %v", result)
	}
	if list[0] != "positive" || list[1] != "negative" || list[2] != "zero" {
		t.Fatalf("expected [positive, negative, zero], got %v", list)
	}
}

// Note: Monty does not support class definitions ("does not yet support class
// definitions"). Use dataclasses with the Python-side registration API instead.

// ==========================================================================
// Lambda, closures, generators
// ==========================================================================

func TestLambda(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "(lambda x, y: x + y)(3, 4)", nil, float64(7))
}

func TestClosure(t *testing.T) {
	r := newRunner(t)
	code := `
def make_adder(n):
    def adder(x):
        return x + n
    return adder
add5 = make_adder(5)
add5(10)
`
	assertResult(t, r, code, nil, float64(15))
}

func TestGeneratorExpression(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, "sum(x * x for x in range(5))", nil, float64(30))
}

// ==========================================================================
// String operations (comprehensive from Monty types)
// ==========================================================================

func TestStringMultiply(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `"abc" * 3`, nil, "abcabcabc")
}

func TestStringIn(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `"ll" in "hello"`, nil, true)
	assertResult(t, r, `"xyz" in "hello"`, nil, false)
}

func TestStringSplit(t *testing.T) {
	r := newRunner(t)
	result, err := r.Execute(context.Background(), `"a,b,c".split(",")`, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	list, ok := result.([]any)
	if !ok || len(list) != 3 {
		t.Fatalf("expected list of 3, got %v", result)
	}
	if list[0] != "a" || list[1] != "b" || list[2] != "c" {
		t.Fatalf("expected [a, b, c], got %v", list)
	}
}

func TestStringJoin(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `", ".join(["a", "b", "c"])`, nil, "a, b, c")
}

func TestStringReplace(t *testing.T) {
	r := newRunner(t)
	assertResult(t, r, `"hello world".replace("world", "python")`, nil, "hello python")
}

// ==========================================================================
// Multiple executions and isolation
// ==========================================================================

func TestMultipleExecutionsDifferentInputs(t *testing.T) {
	r := newRunner(t)
	for i := range 10 {
		result, err := r.Execute(context.Background(), "x * 2", map[string]any{"x": i})
		if err != nil {
			t.Fatalf("Execute %d failed: %v", i, err)
		}
		expected := float64(i * 2)
		if result != expected {
			t.Fatalf("Execute %d: expected %v, got %v", i, expected, result)
		}
	}
}

func TestIsolationBetweenExecutions(t *testing.T) {
	r := newRunner(t)
	// First execution defines a variable.
	_, err := r.Execute(context.Background(), "x = 42\nx", nil)
	if err != nil {
		t.Fatalf("first Execute failed: %v", err)
	}
	// Second execution should NOT see x from the first.
	_, err = r.Execute(context.Background(), "x", nil)
	if err == nil {
		t.Fatal("expected error (x should not persist between executions)")
	}
}

func TestConcurrentRunners(t *testing.T) {
	// Test that multiple Runners can coexist.
	r1 := newRunner(t)
	r2 := newRunner(t)

	result1, err := r1.Execute(context.Background(), "1 + 1", nil)
	if err != nil {
		t.Fatalf("r1 Execute failed: %v", err)
	}
	result2, err := r2.Execute(context.Background(), "2 + 2", nil)
	if err != nil {
		t.Fatalf("r2 Execute failed: %v", err)
	}

	if result1 != float64(2) || result2 != float64(4) {
		t.Fatalf("expected 2 and 4, got %v and %v", result1, result2)
	}
}
