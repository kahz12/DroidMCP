// Command github provides an MCP server for interacting with GitHub.
// It uses OAuth2 for authentication and the official google/go-github client.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/google/go-github/v60/github"
	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/oauth2"
)

var (
	cfg      *config.Config
	ghClient *github.Client
)

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	// GITHUB_TOKEN is required for all operations.
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		logger.Fatal("GITHUB_TOKEN environment variable is required", nil)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	// Initialize the GitHub client with authenticated OAuth2 transport.
	ghClient = github.NewClient(tc)

	server := core.NewDroidServer("mcp-github", "1.0.0")
	server.APIKey = config.ResolveAPIKey("github")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	// list_repos: Returns a list of repositories accessible by the token.
	listReposTool := mcp.NewTool("list_repos",
		mcp.WithDescription("List repositories for the authenticated user"),
	)
	s.MCPServer.AddTool(listReposTool, handleListRepos)

	// get_repo: Fetches detailed metadata for a specific repository.
	getRepoTool := mcp.NewTool("get_repo",
		mcp.WithDescription("Get detailed information about a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
	)
	s.MCPServer.AddTool(getRepoTool, handleGetRepo)

	// create_issue: Basic issue tracking support.
	createIssueTool := mcp.NewTool("create_issue",
		mcp.WithDescription("Create a new issue in a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("title", mcp.Required(), mcp.Description("Title of the issue")),
		mcp.WithString("body", mcp.Description("Body content of the issue")),
	)
	s.MCPServer.AddTool(createIssueTool, handleCreateIssue)

	// list_issues: Filtered issue listing.
	listIssuesTool := mcp.NewTool("list_issues",
		mcp.WithDescription("List issues in a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("state", mcp.Description("State of issues to list (open, closed, all). Default: open")),
	)
	s.MCPServer.AddTool(listIssuesTool, handleListIssues)

	// get_file: Retrieves raw file content. Handles Base64 decoding internally.
	getFileTool := mcp.NewTool("get_file",
		mcp.WithDescription("Get the contents of a file from a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file in the repository")),
		mcp.WithString("ref", mcp.Description("The name of the commit/branch/tag. Default: the repository’s default branch")),
	)
	s.MCPServer.AddTool(getFileTool, handleGetFile)

	// get_pr: Detailed Pull Request inspection.
	getPRTool := mcp.NewTool("get_pr",
		mcp.WithDescription("Get detailed information about a pull request"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithNumber("number", mcp.Required(), mcp.Description("Pull request number")),
	)
	s.MCPServer.AddTool(getPRTool, handleGetPR)

	// create_pr: Simplifies the PR creation workflow.
	createPRTool := mcp.NewTool("create_pr",
		mcp.WithDescription("Create a new pull request"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("title", mcp.Required(), mcp.Description("Title of the pull request")),
		mcp.WithString("head", mcp.Required(), mcp.Description("The name of the branch where your changes are implemented")),
		mcp.WithString("base", mcp.Required(), mcp.Description("The name of the branch you want the changes pulled into")),
		mcp.WithString("body", mcp.Description("Body content of the pull request")),
	)
	s.MCPServer.AddTool(createPRTool, handleCreatePR)

	// commit_file: High-level Content API for single-file commits.
	// This automatically handles SHA management for updates.
	commitFileTool := mcp.NewTool("commit_file",
		mcp.WithDescription("Create or update a file in a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file in the repository")),
		mcp.WithString("content", mcp.Required(), mcp.Description("New content of the file")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Commit message")),
		mcp.WithString("branch", mcp.Description("The branch name. Default: the repository’s default branch")),
	)
	s.MCPServer.AddTool(commitFileTool, handleCommitFile)
}

func handleListRepos(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repos, _, err := ghClient.Repositories.List(ctx, "", nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var result string
	for _, r := range repos {
		result += fmt.Sprintf("- %s: %s\n", r.GetFullName(), r.GetDescription())
	}
	return mcp.NewToolResultText(result), nil
}

func handleGetRepo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, _ := req.RequireString("owner")
	repo, _ := req.RequireString("repo")

	r, _, err := ghClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	res := fmt.Sprintf("Name: %s\nDescription: %s\nStars: %d\nForks: %d\nOpen Issues: %d\nURL: %s",
		r.GetFullName(), r.GetDescription(), r.GetStargazersCount(), r.GetForksCount(), r.GetOpenIssuesCount(), r.GetHTMLURL())
	return mcp.NewToolResultText(res), nil
}

func handleCreateIssue(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, _ := req.RequireString("owner")
	repo, _ := req.RequireString("repo")
	title, _ := req.RequireString("title")
	body := req.GetString("body", "")

	issueRequest := &github.IssueRequest{
		Title: &title,
		Body:  &body,
	}

	issue, _, err := ghClient.Issues.Create(ctx, owner, repo, issueRequest)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Issue created successfully: %s", issue.GetHTMLURL())), nil
}

func handleListIssues(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, _ := req.RequireString("owner")
	repo, _ := req.RequireString("repo")
	state := req.GetString("state", "open")

	opts := &github.IssueListByRepoOptions{
		State: state,
	}

	issues, _, err := ghClient.Issues.ListByRepo(ctx, owner, repo, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var result string
	for _, i := range issues {
		result += fmt.Sprintf("#%d: %s (%s)\n", i.GetNumber(), i.GetTitle(), i.GetState())
	}
	if result == "" {
		result = "No issues found."
	}
	return mcp.NewToolResultText(result), nil
}

func handleGetFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, _ := req.RequireString("owner")
	repo, _ := req.RequireString("repo")
	path, _ := req.RequireString("path")
	ref := req.GetString("ref", "")

	opts := &github.RepositoryContentGetOptions{
		Ref: ref,
	}

	fileContent, _, _, err := ghClient.Repositories.GetContents(ctx, owner, repo, path, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// GitHub returns content encoded in Base64 for many files.
	// GetContent() decodes it automatically.
	content, err := fileContent.GetContent()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(content), nil
}

func handleGetPR(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, _ := req.RequireString("owner")
	repo, _ := req.RequireString("repo")
	number, _ := req.RequireInt("number")

	pr, _, err := ghClient.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	res := fmt.Sprintf("PR #%d: %s\nState: %s\nUser: %s\nBody: %s\nURL: %s",
		pr.GetNumber(), pr.GetTitle(), pr.GetState(), pr.GetUser().GetLogin(), pr.GetBody(), pr.GetHTMLURL())
	return mcp.NewToolResultText(res), nil
}

func handleCreatePR(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, _ := req.RequireString("owner")
	repo, _ := req.RequireString("repo")
	title, _ := req.RequireString("title")
	head, _ := req.RequireString("head")
	base, _ := req.RequireString("base")
	body := req.GetString("body", "")

	newPR := &github.NewPullRequest{
		Title: &title,
		Head:  &head,
		Base:  &base,
		Body:  &body,
	}

	pr, _, err := ghClient.PullRequests.Create(ctx, owner, repo, newPR)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Pull request created successfully: %s", pr.GetHTMLURL())), nil
}

func handleCommitFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, _ := req.RequireString("owner")
	repo, _ := req.RequireString("repo")
	path, _ := req.RequireString("path")
	content, _ := req.RequireString("content")
	message, _ := req.RequireString("message")
	branch := req.GetString("branch", "")

	// Check if file exists to get SHA. SHA is MANDATORY for updating existing files.
	getOpts := &github.RepositoryContentGetOptions{Ref: branch}
	fileContent, _, _, err := ghClient.Repositories.GetContents(ctx, owner, repo, path, getOpts)

	var sha *string
	if err == nil && fileContent != nil {
		sha = fileContent.SHA
	}

	opts := &github.RepositoryContentFileOptions{
		Message: github.String(message),
		Content: []byte(content),
		SHA:     sha,
	}
	if branch != "" {
		opts.Branch = github.String(branch)
	}

	_, _, err = ghClient.Repositories.CreateFile(ctx, owner, repo, path, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("File %s committed successfully", path)), nil
}
