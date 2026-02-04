package cifixer

// EvalTestCase represents a test case for evaluating the CI fixer agent.
type EvalTestCase struct {
	Name         string
	Description  string
	Files        map[string]string
	CILogs       string
	ExpectedFix  map[string]string
	Criterion    string
	Deterministic bool
}

// EvalTestCases contains test cases for evaluating the CI fixer agent.
// These are used for offline evaluation before integrating with GitHub.
var EvalTestCases = []EvalTestCase{
	{
		Name:        "go-missing-return-type",
		Description: "Go function missing return type annotation",
		Files: map[string]string{
			"main.go": `package main

func add(a, b int) {
	return a + b
}

func main() {
	result := add(1, 2)
	println(result)
}
`,
		},
		CILogs: `main.go:4:2: too many return values
        have (int)
        want ()`,
		ExpectedFix: map[string]string{
			"main.go": `package main

func add(a, b int) int {
	return a + b
}

func main() {
	result := add(1, 2)
	println(result)
}
`,
		},
		Criterion:    "Fix should add the missing 'int' return type to the function signature without changing any other code",
		Deterministic: true,
	},
	{
		Name:        "go-missing-import",
		Description: "Go file using fmt without importing it",
		Files: map[string]string{
			"main.go": `package main

func main() {
	fmt.Println("hello world")
}
`,
		},
		CILogs: `main.go:4:2: undefined: fmt`,
		ExpectedFix: map[string]string{
			"main.go": `package main

import "fmt"

func main() {
	fmt.Println("hello world")
}
`,
		},
		Criterion:    "Fix should add the missing 'fmt' import without changing any other code",
		Deterministic: true,
	},
	{
		Name:        "go-type-mismatch",
		Description: "Go type mismatch in assignment",
		Files: map[string]string{
			"main.go": `package main

func main() {
	var count int
	count = "5"
	println(count)
}
`,
		},
		CILogs: `main.go:5:9: cannot use "5" (untyped string constant) as int value in assignment`,
		ExpectedFix: map[string]string{
			"main.go": `package main

func main() {
	var count int
	count = 5
	println(count)
}
`,
		},
		Criterion:    "Fix should change the string '5' to integer 5 without changing any other code",
		Deterministic: true,
	},
	{
		Name:        "go-unused-import",
		Description: "Go file with unused import",
		Files: map[string]string{
			"main.go": `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("hello")
}
`,
		},
		CILogs: `main.go:5:2: "os" imported and not used`,
		ExpectedFix: map[string]string{
			"main.go": `package main

import (
	"fmt"
)

func main() {
	fmt.Println("hello")
}
`,
		},
		Criterion:    "Fix should remove the unused 'os' import without changing any other code",
		Deterministic: true,
	},
	{
		Name:        "go-missing-error-check",
		Description: "Go function ignoring error return value",
		Files: map[string]string{
			"main.go": `package main

import (
	"os"
)

func main() {
	f, _ := os.Open("test.txt")
	defer f.Close()
}
`,
		},
		CILogs: `main.go:8:5: Error return value of 'os.Open' is not checked (errcheck)`,
		ExpectedFix: map[string]string{
			"main.go": `package main

import (
	"log"
	"os"
)

func main() {
	f, err := os.Open("test.txt")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
}
`,
		},
		Criterion:    "Fix should properly handle the error from os.Open with appropriate error handling",
		Deterministic: false,
	},
	{
		Name:        "typescript-missing-type",
		Description: "TypeScript function parameter without type annotation",
		Files: map[string]string{
			"src/utils.ts": `export function greet(name) {
  return "Hello, " + name;
}
`,
		},
		CILogs: `src/utils.ts:1:24 - error TS7006: Parameter 'name' implicitly has an 'any' type.`,
		ExpectedFix: map[string]string{
			"src/utils.ts": `export function greet(name: string) {
  return "Hello, " + name;
}
`,
		},
		Criterion:    "Fix should add the string type annotation to the name parameter",
		Deterministic: true,
	},
	{
		Name:        "python-indentation-error",
		Description: "Python file with indentation error",
		Files: map[string]string{
			"app.py": `def hello():
    print("hello")
   print("world")  # wrong indentation
`,
		},
		CILogs: `  File "app.py", line 3
    print("world")
    ^
IndentationError: unindent does not match any outer indentation level`,
		ExpectedFix: map[string]string{
			"app.py": `def hello():
    print("hello")
    print("world")
`,
		},
		Criterion:     "Fix should correct the indentation of line 3 to match line 2",
		Deterministic: true,
	},
	{
		Name:        "go-test-failure",
		Description: "Go test failing due to incorrect implementation",
		Files: map[string]string{
			"math.go": `package math

func Add(a, b int) int {
	return a - b // bug: should be +
}
`,
			"math_test.go": `package math

import "testing"

func TestAdd(t *testing.T) {
	result := Add(2, 3)
	if result != 5 {
		t.Errorf("Add(2, 3) = %d; want 5", result)
	}
}
`,
		},
		CILogs: `--- FAIL: TestAdd (0.00s)
    math_test.go:8: Add(2, 3) = -1; want 5
FAIL`,
		ExpectedFix: map[string]string{
			"math.go": `package math

func Add(a, b int) int {
	return a + b
}
`,
		},
		Criterion:    "Fix should change the subtraction operator to addition in math.go, not modify the test",
		Deterministic: true,
	},
	{
		Name:        "go-multiple-errors",
		Description: "Go file with multiple type errors",
		Files: map[string]string{
			"main.go": `package main

func process(data string) int {
	length := len(data)
	return length
}

func main() {
	result := process(42)
	fmt.Println(result)
}
`,
		},
		CILogs: `main.go:9:19: cannot use 42 (untyped int constant) as string value in argument to process
main.go:10:2: undefined: fmt`,
		ExpectedFix: map[string]string{
			"main.go": `package main

import "fmt"

func process(data string) int {
	length := len(data)
	return length
}

func main() {
	result := process("42")
	fmt.Println(result)
}
`,
		},
		Criterion:    "Fix should add the fmt import and change 42 to \"42\" to fix both errors",
		Deterministic: true,
	},
	{
		Name:        "go-lint-exported-function",
		Description: "Go exported function without documentation comment",
		Files: map[string]string{
			"api.go": `package api

func ProcessRequest(req string) string {
	return "processed: " + req
}
`,
		},
		CILogs: `api.go:3:1: exported function ProcessRequest should have comment or be unexported (golint)`,
		ExpectedFix: map[string]string{
			"api.go": `package api

// ProcessRequest processes the given request string.
func ProcessRequest(req string) string {
	return "processed: " + req
}
`,
		},
		Criterion:    "Fix should add a documentation comment for the exported function",
		Deterministic: false,
	},
}
