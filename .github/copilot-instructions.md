# goLibMyCarrier AI Development Instructions

## Additional Instructions

**You MUST also follow all applicable instruction files in `.github/instructions/`:**
- [go.instructions.md](.github/instructions/go.instructions.md) - Idiomatic Go patterns, naming conventions, error handling

For the slippy package specifically, read [slippy/CLAUDE.md](slippy/CLAUDE.md) for shadow mode, migration, and client patterns.

## Architecture Overview

This is a **multi-module Go monorepo** where each subdirectory is an independent Go module with its own `go.mod`. All modules share unified versioning via CI (e.g., `clickhouse/v1.3.43`, `slippy/v1.3.43`).

### Key Packages
- **clickhouse** - ClickHouse client with retry logic and session management
- **slippy** - Routing slip orchestration for CI/CD pipelines (most complex - see `slippy/CLAUDE.md`)
- **logger** - Zap-based structured logging with `LOG_LEVEL` and `LOG_APP_NAME` env vars
- **kafka**, **vault**, **github**, **auth**, **otel** - Infrastructure integrations

## Validation Workflow

**CRITICAL: Never bypass linting rules.** Do not use `//nolint` directives, `_ =` to ignore errors, or any other mechanism to circumvent lint errors without explicit user permission. Always fix the underlying issue properly.

**CRITICAL: Update existing functions, never create new versions unless explicitly instructed to do so.** When changing a function's signature or behavior, modify the existing function directly. Never create variants like `FooWithWarnings`, `FooV2`, or `FooNew`. Update all call sites and tests to use the new signature. Use result structs to add return values without breaking callers.

Always keep the repo in a clean, validated state before handing work back. Run **repo-wide** commands when changes span multiple modules:

- `make fmt`
- `make tidy`
- `make lint`
- `make test`
- `make check-sec` (security sweep when touching dependencies or external inputs)

When work is limited to a single module, you may additionally run the targeted variants (`make fmt PKG=<module>`, `make lint PKG=<module>`, `make test PKG=<module>`), but the repo-wide suite above must still be green before completion. Lint settings live in `.github/.golangci.yml`; rely on that config instead of ad-hoc rules. Every command listed here must finish with **zero errors** before you consider any task complete.

## Critical Patterns

### 1. Config Loading with Error Capture
Use viper with defaults; capture load errors immediately rather than retrying in validation:

```go
// Good: Capture error on first attempt
chConfig, err := ch.ClickhouseLoadConfig()
if err != nil {
    cfg.clickhouseLoadErr = err  // Store for later surfacing
} else {
    cfg.ClickHouseConfig = chConfig
}

// Bad: Silent swallowing with retry later
if chConfig, err := ch.ClickhouseLoadConfig(); err == nil {
    cfg.ClickHouseConfig = chConfig
}  // Error lost!
```

### 2. Interface & Test Conventions
Follow the shared guidance in `.github/instructions/go.instructions.md` for interface design, mocking strategy, and the `z_*.go` test file ordering pattern. Keep any repo-specific deviations documented there.

### 3. Environment Variable Defaults
Optional vars should have `viper.SetDefault()` before unmarshaling:

```go
v := viper.New()  // Always use isolated instance in library code
v.SetEnvPrefix("MYPREFIX")
v.SetDefault("port", "9440")
v.SetDefault("skipverify", "false")
v.AutomaticEnv()
v.Unmarshal(&config)  // Defaults apply if env not set
```

### 4. Isolated Viper Instances (CRITICAL)
**Never use global viper functions in library packages.** Global state causes cross-package interference when multiple packages use different env prefixes.

```go
// Good: Isolated instance - does not affect other packages
func LoadConfig() (*Config, error) {
    v := viper.New()
    v.SetEnvPrefix("CLICKHOUSE")
    v.BindEnv("hostname", "CLICKHOUSE_HOSTNAME")
    // ...
}

// Bad: Global state - breaks other packages using viper
func LoadConfig() (*Config, error) {
    viper.SetEnvPrefix("CLICKHOUSE")  // Affects ALL viper.GetString() calls!
    viper.BindEnv("hostname", "CLICKHOUSE_HOSTNAME")
    // ...
}
```

## Slippy-Specific Guidance

Highlights from the slippy instructions (see Additional Instructions for the link) that most often impact changes:
- **Shadow mode** (`SLIPPY_SHADOW_MODE`) controls blocking vs non-blocking error handling
- **Validate-first migrations** - check schema version before running migrations
- **Nil client safety** - all operations must handle nil client gracefully
- **Correlation ID** is the canonical slip identifier throughout lifecycle

## CI/Release Process

- GitVersion calculates semantic versions from commit history
- Release creates tags for root module (`v1.3.43`) AND all submodules (`slippy/v1.3.43`)
- pkg.go.dev refresh is automatic post-release
- **75% test coverage threshold** enforced per module

## Project State Documentation

**You MUST maintain `.github/PROJECT_STATE.md` as working memory for this project.**

### At Session Start
- **Read `.github/PROJECT_STATE.md`** to understand current state, recent changes, and pending work
- Review any in-progress items or architectural decisions

### During Work
Update PROJECT_STATE.md whenever:
- Implementing new features or systems
- Making architectural decisions or changes
- Discovering technical debt or issues
- Completing significant milestones
- User provides direction that affects project structure

### Required Sections
```markdown
# Project State — goLibMyCarrier

> **Last Updated:** [Date]
> **Status:** [Brief current state summary]

## Overview
[What this library does, key characteristics]

## Implemented Systems
[Per-module breakdown of completed functionality]

## Recent Changes
[What changed in recent sessions, new components/systems/patterns]

## Current Focus
[What's being worked on now, immediate next steps]

## Architectural Decisions
[Key design choices made, rationale, tradeoffs]

## Technical Debt / Known Issues
[Outstanding problems, things to fix later]

## Next Steps (Not Yet Implemented)
[Planned features, improvements, user requests pending]
```

### Update Discipline
- Keep entries **concise but specific** (reference file paths, function names)
- **Date entries** in Recent Changes section
- Remove outdated items from "Next Steps" as they're completed
- If PROJECT_STATE.md doesn't exist, **create it** with current project state

# Code Review Guidelines

## Review Language

When performing a code review, respond in **English** (or specify your preferred language).

## Review Priorities

When performing a code review, prioritize issues in the following order:

### 🔴 CRITICAL (Block merge)
- **Security**: Vulnerabilities, exposed secrets, authentication/authorization issues
- **Correctness**: Logic errors, data corruption risks, race conditions
- **Breaking Changes**: API contract changes without versioning
- **Data Loss**: Risk of data loss or corruption

### 🟡 IMPORTANT (Requires discussion)
- **Code Quality**: Severe violations of SOLID principles, excessive duplication
- **Test Coverage**: Missing tests for critical paths or new functionality
- **Performance**: Obvious performance bottlenecks (N+1 queries, memory leaks)
- **Architecture**: Significant deviations from established patterns

### 🟢 SUGGESTION (Non-blocking improvements)
- **Readability**: Poor naming, complex logic that could be simplified
- **Optimization**: Performance improvements without functional impact
- **Best Practices**: Minor deviations from conventions
- **Documentation**: Missing or incomplete comments/documentation

## General Review Principles

When performing a code review, follow these principles:

1. **Be specific**: Reference exact lines, files, and provide concrete examples
2. **Provide context**: Explain WHY something is an issue and the potential impact
3. **Suggest solutions**: Show corrected code when applicable, not just what's wrong
4. **Be constructive**: Focus on improving the code, not criticizing the author
5. **Recognize good practices**: Acknowledge well-written code and smart solutions
6. **Be pragmatic**: Not every suggestion needs immediate implementation
7. **Group related comments**: Avoid multiple comments about the same topic

## Code Quality Standards

When performing a code review, check for:

### Clean Code
- Descriptive and meaningful names for variables, functions, and classes
- Single Responsibility Principle: each function/class does one thing well
- DRY (Don't Repeat Yourself): no code duplication
- Functions should be small and focused (ideally < 20-30 lines)
- Avoid deeply nested code (max 3-4 levels)
- Avoid magic numbers and strings (use constants)
- Code should be self-documenting; comments only when necessary

### Examples
```go
// ❌ BAD: Poor naming and magic numbers
func calc(x, y float64) float64 {
	if x > 100 {
		return y * 0.15
	}
	return y * 0.10
}

// ✅ GOOD: Clear naming and constants
const (
	premiumThreshold    = 100.0
	premiumDiscountRate = 0.15
	standardDiscountRate = 0.10
)

func calculateDiscount(orderTotal, itemPrice float64) float64 {
	discountRate := standardDiscountRate
	if orderTotal > premiumThreshold {
		discountRate = premiumDiscountRate
	}
	return itemPrice * discountRate
}
```

### Error Handling
- Proper error handling at appropriate levels
- Meaningful error messages
- No silent failures or ignored exceptions
- Fail fast: validate inputs early
- Use appropriate error types/exceptions

### Examples
```go
// ❌ BAD: Silent failure and generic error
func processUser(userID int) {
	user, err := db.Get(userID)
	if err != nil {
		return // silently ignored
	}
	_ = user.Process() // error discarded
}

// ✅ GOOD: Explicit error handling
func processUser(userID int) error {
	if userID <= 0 {
		return fmt.Errorf("invalid userID: %d", userID)
	}

	user, err := db.Get(userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return fmt.Errorf("user %d not found in database: %w", userID, err)
		}
		return fmt.Errorf("failed to retrieve user %d: %w", userID, err)
	}

	return user.Process()
}
```

## Security Review

When performing a code review, check for security issues:

- **Sensitive Data**: No passwords, API keys, tokens, or PII in code or logs
- **Input Validation**: All user inputs are validated and sanitized
- **SQL Injection**: Use parameterized queries, never string concatenation
- **Authentication**: Proper authentication checks before accessing resources
- **Authorization**: Verify user has permission to perform action
- **Cryptography**: Use established libraries, never roll your own crypto
- **Dependency Security**: Check for known vulnerabilities in dependencies

### Examples
```go
// ❌ BAD: SQL injection vulnerability
query := "SELECT * FROM users WHERE email = '" + email + "'"
rows, err := db.Query(query)

// ✅ GOOD: Parameterized query
rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE email = ?", email)
```

```go
// ❌ BAD: Exposed secret in code
const apiKey = "sk_live_abc123xyz789"

// ✅ GOOD: Use environment variables
apiKey := os.Getenv("API_KEY")
```

## Testing Standards

When performing a code review, verify test quality:

- **Coverage**: Critical paths and new functionality must have tests
- **Test Names**: Descriptive names that explain what is being tested
- **Test Structure**: Clear Arrange-Act-Assert or Given-When-Then pattern
- **Independence**: Tests should not depend on each other or external state
- **Assertions**: Use specific assertions, avoid generic assertTrue/assertFalse
- **Edge Cases**: Test boundary conditions, null values, empty collections
- **Mock Appropriately**: Mock external dependencies, not domain logic

### Examples
```go
// ❌ BAD: Vague name and assertion
func Test1(t *testing.T) {
	result := calc(5, 10)
	if result == 0 {
		t.Fail()
	}
}

// ✅ GOOD: Descriptive name and specific assertion
func TestCalculateDiscount_StandardRate_ForOrdersUnder100(t *testing.T) {
	orderTotal := 50.0
	itemPrice := 20.0

	discount := calculateDiscount(orderTotal, itemPrice)

	if discount != 2.00 {
		t.Errorf("expected discount of 2.00, got %.2f", discount)
	}
}
```

## Performance Considerations

When performing a code review, check for performance issues:

- **Database Queries**: Avoid N+1 queries, use proper indexing
- **Algorithms**: Appropriate time/space complexity for the use case
- **Caching**: Utilize caching for expensive or repeated operations
- **Resource Management**: Proper cleanup of connections, files, streams
- **Pagination**: Large result sets should be paginated
- **Lazy Loading**: Load data only when needed

### Examples
```go
// ❌ BAD: N+1 query problem
rows, _ := db.QueryContext(ctx, "SELECT id, name FROM users")
for rows.Next() {
	var user User
	rows.Scan(&user.ID, &user.Name)
	// N+1! One query per user inside the loop
	orders, _ := db.QueryContext(ctx, "SELECT * FROM orders WHERE user_id = ?", user.ID)
}

// ✅ GOOD: Use a JOIN to fetch in a single query
rows, _ := db.QueryContext(ctx,
	"SELECT u.id, u.name, o.id, o.total FROM users u LEFT JOIN orders o ON u.id = o.user_id")
for rows.Next() {
	var u User
	var o Order
	rows.Scan(&u.ID, &u.Name, &o.ID, &o.Total)
}
```

## Architecture and Design

When performing a code review, verify architectural principles:

- **Separation of Concerns**: Clear boundaries between layers/modules
- **Dependency Direction**: High-level modules don't depend on low-level details
- **Interface Segregation**: Prefer small, focused interfaces
- **Loose Coupling**: Components should be independently testable
- **High Cohesion**: Related functionality grouped together
- **Consistent Patterns**: Follow established patterns in the codebase

## Documentation Standards

When performing a code review, check documentation:

- **API Documentation**: Public APIs must be documented (purpose, parameters, returns)
- **Complex Logic**: Non-obvious logic should have explanatory comments
- **README Updates**: Update README when adding features or changing setup
- **Breaking Changes**: Document any breaking changes clearly
- **Examples**: Provide usage examples for complex features

## Comment Format Template

When performing a code review, use this format for comments:

```markdown
**[PRIORITY] Category: Brief title**

Detailed description of the issue or suggestion.

**Why this matters:**
Explanation of the impact or reason for the suggestion.

**Suggested fix:**
[code example if applicable]

**Reference:** [link to relevant documentation or standard]
```

### Example Comments

#### Critical Issue
````markdown
**🔴 CRITICAL - Security: SQL Injection Vulnerability**

The query on line 45 concatenates user input directly into the SQL string,
creating a SQL injection vulnerability.

**Why this matters:**
An attacker could manipulate the email parameter to execute arbitrary SQL commands,
potentially exposing or deleting all database data.

**Suggested fix:**
```go
// Instead of:
query := "SELECT * FROM users WHERE email = '" + email + "'"

// Use parameterized queries:
rows, err := db.QueryContext(ctx, "SELECT * FROM users WHERE email = ?", email)
```

**Reference:** OWASP SQL Injection Prevention Cheat Sheet
````

#### Important Issue
````markdown
**🟡 IMPORTANT - Testing: Missing test coverage for critical path**

The `processPayment()` function handles financial transactions but has no tests
for the refund scenario.

**Why this matters:**
Refunds involve money movement and should be thoroughly tested to prevent
financial errors or data inconsistencies.

**Suggested fix:**
Add test case:
```go
func TestProcessPayment_FullRefund_WhenOrderCancelled(t *testing.T) {
	order := createOrder(100.0, StatusCancelled)

	result, err := processPayment(order, PaymentOpts{Type: Refund})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RefundAmount != 100.0 {
		t.Errorf("expected refund of 100, got %.2f", result.RefundAmount)
	}
	if result.Status != StatusRefunded {
		t.Errorf("expected status refunded, got %s", result.Status)
	}
}
```
````

#### Suggestion
````markdown
**🟢 SUGGESTION - Readability: Simplify nested conditionals**

The nested if statements on lines 30-40 make the logic hard to follow.

**Why this matters:**
Simpler code is easier to maintain, debug, and test.

**Suggested fix:**
```go
// Instead of nested ifs:
if user != nil {
	if user.IsActive {
		if user.HasPermission("write") {
			// do something
		}
	}
}

// Consider guard clauses:
if user == nil || !user.IsActive || !user.HasPermission("write") {
	return
}
// do something
```
````

## Review Checklist

When performing a code review, systematically verify:

### Code Quality
- [ ] Code follows consistent style and conventions
- [ ] Names are descriptive and follow naming conventions
- [ ] Functions/methods are small and focused
- [ ] No code duplication
- [ ] Complex logic is broken into simpler parts
- [ ] Error handling is appropriate
- [ ] No commented-out code or TODO without tickets

### Security
- [ ] No sensitive data in code or logs
- [ ] Input validation on all user inputs
- [ ] No SQL injection vulnerabilities
- [ ] Authentication and authorization properly implemented
- [ ] Dependencies are up-to-date and secure

### Testing
- [ ] New code has appropriate test coverage
- [ ] Tests are well-named and focused
- [ ] Tests cover edge cases and error scenarios
- [ ] Tests are independent and deterministic
- [ ] No tests that always pass or are commented out

### Performance
- [ ] No obvious performance issues (N+1, memory leaks)
- [ ] Appropriate use of caching
- [ ] Efficient algorithms and data structures
- [ ] Proper resource cleanup

### Architecture
- [ ] Follows established patterns and conventions
- [ ] Proper separation of concerns
- [ ] No architectural violations
- [ ] Dependencies flow in correct direction

### Documentation
- [ ] Public APIs are documented
- [ ] Complex logic has explanatory comments
- [ ] README is updated if needed
- [ ] Breaking changes are documented