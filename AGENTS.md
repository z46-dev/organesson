# Coding Guidelines

These are mandatory coding rules & guidelines for all code in this repository.

## Global Coding Rules

1. Lines of code is not a measure of quality. Do not write code to meet a line count requirement. Instead, focus on clean elegant solutions. Don't reuse code, spend time scanning/indexing to ensure you are not duplicating functionality. If you find yourself writing the same code twice, consider refactoring it into a shared function or module.
2. Writing tests for the fun of it is not a good idea. Write tests for important broad things, not for every single function. If you find yourself writing a test for a function that is not important, consider if the function is even necessary.
3. We should ensure that our entire test plan can be ran inside a GitHub Actions workflow. We can use the Go installable module nektos/act to run the workflow locally. We should run tests, code quality checks, and builds in the workflow.
4. Do not leave dev servers running. Always terminate running processes unless asked to run one after.

## Go Coding Rules

Mandatory coding guidelines for Go:

1. Avoid `:=` as much as possible, only use in loops or select statements
2. Use explicit type declarations for variables in blocks
3. Use named/naked returns in functions to improve readability
4. For all major functions, put a brief one-line comment explaining the function's purpose. If it needs more than a line, expand as needed. Do not over document
5. Blank new lines after `}` when sensible, blank new lines to separate logical blocks of code
6. For checking errors, put the function call into the if statement
7. Use grouped `var (...)` declarations when a block needs more than one local variable
8. For comma-ok checks, put the assignment in the if statement when the value is only needed for that branch or immediate check
9. Avoid redundant `return` statements inside conditionals when the function can naturally fall through to its named return
10. Optimize for readability and performance always. Conciseness is good, but not at the expense of clarity and performance
11. For type declarations, if applicable, create them in a `type ( ... )` block
12. When setting a value you're going to immediately check, do the assignment in the if statement
13. Inline values used exactly once. Do not create one-use locals solely to name literals, filters, function arguments, or struct values; pass the expression directly where it is consumed. Keep a local only when the value is reused, must survive multi-step error handling, or must remain identical across multiple uses

Here's an example:

```go
var (
    count, other int = 1, 3
    something *reflect.Value
    err error
)

type (
    Something struct {
        Param1, Param2 string
        SomeInt        int
    }

    SomethingElse struct {
        Param1, Param2 string
        SomeInt        int
    }
)

// Computes the sum of two integers, returning an error if the result is negative.
func doSomething(a, b int) (c int, err error) {
    if c = a + b; c < 0 {
        err = fmt.Errorf("result is negative")
        c = 0

        // No return here because the return at the end of the function would be literally the same...
    }

    return
}

func main() {
    if count, err = doSomething(1, 2); err != nil {
        fmt.Printf("Error: %v\n", err)
    } else {
        fmt.Printf("Result: %d\n", count)
    }

    something = nil
}
```

## JavaScript/TypeScript, HTML, JSON, and CSS Rules

Mandatory frontend coding guidelines:

1. Keep JavaScript modules focused; split files when they contain unrelated responsibilities
2. Prefer clear, descriptive names over abbreviations
3. Use `const` by default and `let` only when reassignment is required; do not use `var`
4. Keep functions small and avoid deeply nested control flow
5. Reuse shared components, helpers, and styles instead of duplicating behavior or declarations
6. Use semantic HTML elements and preserve keyboard accessibility
7. Keep HTML structure minimal; avoid wrapper elements that do not provide layout, styling, or semantic value
8. Keep CSS selectors shallow and class-based; avoid IDs and excessive specificity for styling
9. Group related CSS declarations consistently and use existing design tokens or custom properties
10. Build responsive layouts intentionally and avoid hard-coded dimensions unless the design requires them
11. Remove unused JavaScript, markup, selectors, and assets when changing related code
12. Optimize for readability, accessibility, maintainability, and browser performance
13. Split large CSS files into smaller tailwind layers or component-specific files when they contain unrelated responsibilities
14. Ensure a clean structure of HTML, CSS, and JavaScript files.
15. Use 4 indent spaces for code formatting.
16. No trailing whitespace or commas.
17. Format arrays/json cleanly:

```js
// Good
const arr = [{
    param1: "value1",
    param2: "value2",
    someInt: 42
}, {
    param1: "value3",
    param2: "value4",
    someInt: 43
}];

// Bad
const arr = [
    {
        param1: "value1",
        param2: "value2",
        someInt: 42,
    },
    {
        param1: "value3",
        param2: "value4",
        someInt: 43,
    }
];
```