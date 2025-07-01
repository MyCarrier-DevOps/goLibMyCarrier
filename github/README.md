# GitHub Handler Library

A comprehensive Go library for interacting with GitHub API and Git repositories using GitHub App credentials. This library provides a clean, extensible API for authentication, repository operations, commit management, and check run creation.

## 🚀 Features

- **🔐 Authentication**: Seamless GitHub App authentication with automatic token management
- **📂 Repository Operations**: Flexible repository cloning with extensive customization options
- **💾 Commit Management**: Advanced commit operations with selective file staging and push control
- **✅ Check Runs**: Create rich GitHub check runs with annotations, actions, and detailed output
- **🎛️ Extensible Design**: Configuration-based approach using option structs for maximum flexibility
- **🔄 Backwards Compatibility**: Simple convenience functions for common use cases
- **🛡️ Robust Error Handling**: Comprehensive error handling with descriptive messages
- **🧪 Well Tested**: Extensive test coverage for all major functionality

## 📋 Requirements

The library expects the following environment variables:

- `GITHUB_APP_PRIVATE_KEY`: The private key of the GitHub App (PEM format)
- `GITHUB_APP_ID`: The App ID of the GitHub App
- `GITHUB_APP_INSTALLATION_ID`: The Installation ID for the GitHub App

## 🛠️ Installation

```bash
go get github.com/MyCarrier-DevOps/goLibMyCarrier/github
```

## 🔧 Quick Start

### Authentication

```go
import "github.com/MyCarrier-DevOps/goLibMyCarrier/github"

// Load configuration from environment variables
config, err := github_handler.GithubLoadConfig()
if err != nil {
    log.Fatalf("Failed to load GitHub configuration: %v", err)
}

// Create a new GitHub session
session, err := github_handler.NewGithubSession(config.Pem, config.AppId, config.InstallId)
if err != nil {
    log.Fatalf("Failed to create GitHub session: %v", err)
}

// Access the authenticated client and token
client := session.Client()
token := session.AuthToken()
```

## 📂 Repository Operations

### Simple Repository Cloning

```go
// Basic repository cloning
repo, err := github_handler.CloneRepositorySimple(
    session,
    "https://github.com/org/repo.git",
    "/local/path",
    "main",
)
if err != nil {
    log.Fatalf("Failed to clone repository: %v", err)
}
```

### No-Checkout Repository Cloning

```go
// Clone without checking out files (useful for CI/CD operations that only need repository metadata)
repo, err := github_handler.CloneRepositoryNoCheckout(
    session,
    "https://github.com/org/repo.git",
    "/local/path",
    "main",
)
if err != nil {
    log.Fatalf("Failed to clone repository: %v", err)
}
// Repository is cloned but no files are checked out to the working directory
```

### Advanced Repository Cloning

```go
// Full configuration control
options := github_handler.CloneOptions{
    RepositoryURL: "https://github.com/org/repo.git",
    WorkDir:       "/custom/path",
    Branch:        "feature-branch",
    SingleBranch:  true,
    Depth:         1,
    NoCheckout:    false, // Set to true to skip file checkout
    Progress:      os.Stdout,
}

repo, err := github_handler.CloneRepository(session, options)
if err != nil {
    log.Fatalf("Failed to clone with options: %v", err)
}
```

### Custom Authentication

```go
// Clone with custom authentication
customAuth := &ghhttp.BasicAuth{
    Username: "x-access-token",
    Password: "your-custom-token",
}

repo, err := github_handler.CloneRepositoryWithAuth(
    session,
    "https://github.com/org/repo.git",
    "/local/path",
    "main",
    customAuth,
    nil, // Use default git options
)
```

## 💾 Commit Operations

### Simple Commit Operations

```go
// Commit specific files with automatic push
commitHash, err := github_handler.CommitChangesWithToken(
    repo,
    "main",
    "Update deployment configuration",
    []string{"deployment.yaml", "config.yaml"},
    "your-github-token",
)
if err != nil {
    log.Fatalf("Failed to commit changes: %v", err)
}
fmt.Printf("Committed changes: %s\n", commitHash)
```

```go
// Commit all changes with automatic push
commitHash, err := github_handler.CommitAllChangesWithToken(
    repo,
    "main",
    "Update all configuration files",
    "your-github-token",
)
if err != nil {
    log.Fatalf("Failed to commit all changes: %v", err)
}
```

### Advanced Commit Configuration

```go
// Complete control over commit operation
options := github_handler.CommitOptions{
    Branch:  "feature-branch",
    Message: "Deploy to production environment",
    Files:   []string{"prod-deployment.yaml", "config.yaml"},
    Author: github_handler.AuthorInfo{
        Name:  "Production Deploy Bot",
        Email: "deploy@mycarrier.io",
    },
    Auth: &ghhttp.BasicAuth{
        Username: "x-access-token",
        Password: "your-github-token",
    },
    RemoteName: "upstream",
    RemoteURL:  "https://github.com/org/repo.git",
}

commitHash, err := github_handler.CommitChanges(repo, options)
if err != nil {
    // Handle error - commit may have succeeded even if push failed
    if strings.Contains(err.Error(), "error pushing changes") {
        fmt.Printf("Commit succeeded but push failed: %s\n", commitHash)
    } else {
        log.Fatalf("Commit failed: %v", err)
    }
}
```

### Local-Only Commits

```go
// Commit without pushing (no Auth provided)
options := github_handler.CommitOptions{
    Branch:  "local-branch",
    Message: "Local development changes",
    AddAll:  true,
    Author:  github_handler.GetDefaultAuthor(),
    // No Auth field = no push operation
}

commitHash, err := github_handler.CommitChanges(repo, options)
if err != nil {
    log.Fatalf("Local commit failed: %v", err)
}
fmt.Printf("Local commit created: %s\n", commitHash)
```

## ✅ Check Run Operations

### Simple Check Run Creation

```go
// Create a basic check run
ctx := context.Background()
err := github_handler.CreateCheckRunSimple(
    ctx,
    session,
    "abc123def456",           // commit SHA
    "my-repository",          // repository name
    "my-organization",        // organization
    "Build Check",            // check name
    "Build Results",          // title
    "success",               // conclusion
    "completed",             // status
    "All tests passed successfully!", // summary
)
if err != nil {
    log.Fatalf("Failed to create check run: %v", err)
}
```

### Advanced Check Run with Rich Content

```go
// Create a comprehensive check run with annotations and actions
ctx := context.Background()
now := time.Now()

options := github_handler.CheckRunOptions{
    // Required fields
    HeadSHA:     "abc123def456",
    Repository:  "my-repository",
    Org:         "my-organization",
    Name:        "Comprehensive Test Suite",
    
    // Rich content
    Title:       "Test Results Summary",
    Conclusion:  "success",
    Status:      "completed",
    Summary:     "All 42 tests passed successfully with 98% code coverage",
    Text:        "### Detailed Results\n\n- Unit Tests: ✅ 35/35 passed\n- Integration Tests: ✅ 7/7 passed\n- Code Coverage: 98.2%",
    
    // External integration
    DetailsURL:  "https://ci.mycompany.com/builds/12345",
    ExternalID:  "build-12345",
    StartedAt:   &now,
    CompletedAt: &now,
    
    // Interactive actions
    Actions: []*github.CheckRunAction{
        {
            Label:       github.Ptr("Re-run Tests"),
            Description: github.Ptr("Re-run the entire test suite"),
            Identifier:  github.Ptr("rerun_tests"),
        },
        {
            Label:       github.Ptr("View Logs"),
            Description: github.Ptr("View detailed build logs"),
            Identifier:  github.Ptr("view_logs"),
        },
    },
    
    // Code annotations
    Annotations: []*github.CheckRunAnnotation{
        {
            Path:            github.Ptr("src/main.go"),
            BlobHRef:        github.Ptr("https://github.com/org/repo/blob/abc123/src/main.go"),
            StartLine:       github.Ptr(45),
            EndLine:         github.Ptr(47),
            AnnotationLevel: github.Ptr("warning"),
            Message:         github.Ptr("Consider extracting this logic into a separate function for better maintainability"),
            Title:           github.Ptr("Code Quality: High Complexity"),
        },
        {
            Path:            github.Ptr("tests/integration_test.go"),
            StartLine:       github.Ptr(123),
            EndLine:         github.Ptr(123),
            AnnotationLevel: github.Ptr("notice"),
            Message:         github.Ptr("This test could benefit from additional edge case coverage"),
            Title:           github.Ptr("Test Coverage Suggestion"),
        },
    },
}

err := github_handler.CreateCheckRun(ctx, session, options)
if err != nil {
    log.Fatalf("Failed to create advanced check run: %v", err)
}
```

## 📊 Configuration Reference

### CloneOptions Structure

```go
type CloneOptions struct {
    RepositoryURL   string              // Required: Repository URL to clone
    WorkDir         string              // Local directory (default: "/work")
    Branch          string              // Specific branch to clone (optional)
    Auth            *ghhttp.BasicAuth   // Custom authentication (optional, uses session token by default)
    GitCloneOptions *git.CloneOptions   // Advanced git options (optional)
    Progress        *os.File            // Progress output destination (default: os.Stdout)
    SingleBranch    bool                // Clone only the specified branch (default: true)
    Depth           int                 // Clone depth for shallow clones (default: 1)
    NoCheckout      bool                // Skip checking out files to working directory (default: false)
}
```

### CommitOptions Structure

```go
type CommitOptions struct {
    Branch     string              // Required: Branch to commit to
    Message    string              // Required: Commit message
    Files      []string            // Specific files to stage (alternative to AddAll)
    AddAll     bool                // Stage all changes in the working directory
    Author     AuthorInfo          // Author information (uses default if not specified)
    Auth       *ghhttp.BasicAuth   // Authentication for push operation (optional)
    RemoteURL  string              // Custom remote URL (optional)
    RemoteName string              // Remote name (default: "origin")
}

type AuthorInfo struct {
    Name  string                   // Author's name
    Email string                   // Author's email address
}
```

### CheckRunOptions Structure

```go
type CheckRunOptions struct {
    // Required fields
    HeadSHA    string              // Git commit SHA
    Repository string              // Repository name
    Org        string              // Organization or user name
    Name       string              // Check run name
    
    // Content fields
    Title       string             // Check run title
    Conclusion  string             // success, failure, neutral, cancelled, timed_out, action_required
    Status      string             // queued, in_progress, completed (default: "completed")
    Summary     string             // Summary text (supports Markdown)
    Text        string             // Detailed description (supports Markdown)
    
    // Integration fields
    DetailsURL  string             // URL to external build/test system
    ExternalID  string             // External system identifier
    StartedAt   *time.Time         // When the check started
    CompletedAt *time.Time         // When the check completed
    
    // Interactive elements
    Actions     []*github.CheckRunAction     // Action buttons
    Images      []*github.CheckRunImage      // Images to display
    Annotations []*github.CheckRunAnnotation // Code annotations
}
```

## 🎯 Best Practices

### Error Handling
- Always check for errors from all library functions
- For commit operations, check if the error relates to push failures vs commit failures
- Use proper error wrapping when handling errors in your application

### Authentication
- Store GitHub App credentials securely using environment variables
- Rotate GitHub App private keys regularly
- Use least-privilege principle for GitHub App permissions

### Repository Operations
- Use shallow clones (depth: 1) for CI/CD operations to save bandwidth
- Specify branches explicitly to avoid unexpected behavior
- Clean up cloned repositories when done to save disk space
- Use `NoCheckout: true` for operations that only need repository metadata (e.g., analyzing commit history, checking branch information)
- Consider no-checkout clones for performance-critical operations where file content is not needed

### Commit Management
- Use descriptive commit messages following conventional commit format
- Set appropriate author information for audit trails
- Consider local-only commits for development workflows

### Check Runs
- Provide meaningful titles and summaries for better developer experience
- Use annotations to highlight specific code issues
- Include links to external systems for detailed logs or reports

## 🚨 Error Handling Examples

```go
// Handling different types of commit errors
commitHash, err := github_handler.CommitChanges(repo, options)
if err != nil {
    if strings.Contains(err.Error(), "error pushing changes") {
        // Commit succeeded but push failed
        fmt.Printf("Commit %s created but push failed: %v\n", commitHash, err)
        // You might want to retry the push or handle this case specifically
    } else if strings.Contains(err.Error(), "either specify files to add or set AddAll") {
        // Configuration error - fix the options
        fmt.Printf("Configuration error: %v\n", err)
    } else {
        // Other errors (network, permissions, etc.)
        fmt.Printf("Commit operation failed: %v\n", err)
    }
}
```

## 🧪 Testing

The library includes comprehensive tests for all major functionality:

```bash
# Run all tests
go test -v ./...

# Run tests with coverage
go test -v -cover ./...

# Run specific test categories
go test -v -run TestCommit
go test -v -run TestClone
go test -v -run TestCheckRun
```

## 📦 Dependencies

This library leverages the following Go packages:

- **[go-github](https://github.com/google/go-github)** - Official GitHub API client library for Go
- **[go-git](https://github.com/go-git/go-git)** - Pure Go implementation of Git (no external dependencies)
- **[go-githubauth](https://github.com/jferrl/go-githubauth)** - GitHub App authentication utilities
- **[golang-jwt](https://github.com/golang-jwt/jwt)** - JWT implementation for GitHub App authentication
- **[spf13/viper](https://github.com/spf13/viper)** - Configuration management library

## 🤝 Contributing

We welcome contributions! Please ensure that:

1. All changes include appropriate tests
2. Code follows Go best practices and passes linting
3. Documentation is updated for new features
4. Breaking changes are clearly documented

## 📄 License

This library is licensed under the MIT License. See the LICENSE file for details.

---

*For more information, issues, or feature requests, please visit our [GitHub repository](https://github.com/MyCarrier-DevOps/goLibMyCarrier).*
