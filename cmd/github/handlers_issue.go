package main

import (
	"context"
	"time"

	"github.com/google/go-github/v60/github"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/mark3labs/mcp-go/mcp"
)

type issueSummary struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	User      string    `json:"user,omitempty"`
	Labels    []string  `json:"labels,omitempty"`
	Comments  int       `json:"comments"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Body      string    `json:"body,omitempty"`
}

type commentSummary struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	User      string    `json:"user,omitempty"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

func registerIssueTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("create_issue",
		mcp.WithDescription("Create a new issue in a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("title", mcp.Required(), mcp.Description("Title of the issue")),
		mcp.WithString("body", mcp.Description("Body content of the issue")),
		mcp.WithArray("labels", mcp.Description("Optional list of label names to apply")),
	), handleCreateIssue)

	s.MCPServer.AddTool(mcp.NewTool("list_issues",
		mcp.WithDescription("List issues in a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("state", mcp.Description("State of issues to list (open, closed, all). Default: open")),
		mcp.WithNumber("per_page", mcp.Description("Results per page (max 100, default 30)")),
		mcp.WithNumber("page", mcp.Description("Page number to retrieve (default 1)")),
	), handleListIssues)

	s.MCPServer.AddTool(mcp.NewTool("comment_issue",
		mcp.WithDescription("Add a comment to an issue or pull request"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithNumber("number", mcp.Required(), mcp.Description("Issue or PR number")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Comment body (markdown)")),
	), handleCommentIssue)

	s.MCPServer.AddTool(mcp.NewTool("close_issue",
		mcp.WithDescription("Close an open issue"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithNumber("number", mcp.Required(), mcp.Description("Issue number")),
		mcp.WithString("state_reason", mcp.Description("Reason: completed (default) or not_planned")),
	), handleCloseIssue)

	s.MCPServer.AddTool(mcp.NewTool("label_issue",
		mcp.WithDescription("Add labels to an issue, or replace its label set entirely"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithNumber("number", mcp.Required(), mcp.Description("Issue number")),
		mcp.WithArray("labels", mcp.Required(), mcp.Description("Label names to apply")),
		mcp.WithBoolean("replace", mcp.Description("If true, replace existing labels; otherwise append")),
	), handleLabelIssue)
}

func handleCreateIssue(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body := req.GetString("body", "")

	issueRequest := &github.IssueRequest{Title: &title, Body: &body}
	if labels := stringArrayArg(req, "labels"); len(labels) > 0 {
		issueRequest.Labels = &labels
	}

	issue, _, err := ghClient.Issues.Create(ctx, owner, repo, issueRequest)
	if err != nil {
		return githubError(err)
	}
	return jsonResult(issueSummaryFrom(issue))
}

func handleListIssues(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	state := req.GetString("state", "open")

	opts := &github.IssueListByRepoOptions{
		State:       state,
		ListOptions: paginationOpts(req),
	}

	issues, resp, err := ghClient.Issues.ListByRepo(ctx, owner, repo, opts)
	if err != nil {
		return githubError(err)
	}

	items := make([]issueSummary, 0, len(issues))
	for _, i := range issues {
		items = append(items, issueSummaryFrom(i))
	}
	return jsonResult(listResponse[issueSummary]{Items: items, Count: len(items), RateLimit: rateOf(resp)})
}

func handleCommentIssue(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	number, err := req.RequireInt("number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, err := req.RequireString("body")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	c, _, err := ghClient.Issues.CreateComment(ctx, owner, repo, number, &github.IssueComment{Body: &body})
	if err != nil {
		return githubError(err)
	}
	return jsonResult(commentSummary{
		ID:        c.GetID(),
		Body:      c.GetBody(),
		User:      c.GetUser().GetLogin(),
		HTMLURL:   c.GetHTMLURL(),
		CreatedAt: c.GetCreatedAt().Time,
	})
}

func handleCloseIssue(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	number, err := req.RequireInt("number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	closed := "closed"
	patch := &github.IssueRequest{State: &closed}
	if reason := req.GetString("state_reason", ""); reason != "" {
		patch.StateReason = &reason
	}

	issue, _, err := ghClient.Issues.Edit(ctx, owner, repo, number, patch)
	if err != nil {
		return githubError(err)
	}
	return jsonResult(issueSummaryFrom(issue))
}

func handleLabelIssue(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	number, err := req.RequireInt("number")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	labels := stringArrayArg(req, "labels")
	if len(labels) == 0 {
		return mcp.NewToolResultError("labels must be a non-empty array of strings"), nil
	}

	var (
		applied []*github.Label
		err2    error
	)
	if req.GetBool("replace", false) {
		applied, _, err2 = ghClient.Issues.ReplaceLabelsForIssue(ctx, owner, repo, number, labels)
	} else {
		applied, _, err2 = ghClient.Issues.AddLabelsToIssue(ctx, owner, repo, number, labels)
	}
	if err2 != nil {
		return githubError(err2)
	}

	names := make([]string, 0, len(applied))
	for _, l := range applied {
		names = append(names, l.GetName())
	}
	return jsonResult(map[string]any{"labels": names, "count": len(names)})
}

// stringArrayArg pulls a []string out of the JSON arguments. Missing or
// non-array values return an empty slice. Non-string entries are skipped.
func stringArrayArg(req mcp.CallToolRequest, name string) []string {
	raw, ok := req.GetArguments()[name]
	if !ok || raw == nil {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func issueSummaryFrom(i *github.Issue) issueSummary {
	if i == nil {
		return issueSummary{}
	}
	out := issueSummary{
		Number:    i.GetNumber(),
		Title:     i.GetTitle(),
		State:     i.GetState(),
		HTMLURL:   i.GetHTMLURL(),
		User:      i.GetUser().GetLogin(),
		Comments:  i.GetComments(),
		CreatedAt: i.GetCreatedAt().Time,
		UpdatedAt: i.GetUpdatedAt().Time,
		Body:      i.GetBody(),
	}
	if len(i.Labels) > 0 {
		out.Labels = make([]string, 0, len(i.Labels))
		for _, l := range i.Labels {
			out.Labels = append(out.Labels, l.GetName())
		}
	}
	return out
}
