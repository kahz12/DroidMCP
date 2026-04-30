package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v60/github"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/mark3labs/mcp-go/mcp"
)

type prSummary struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	HTMLURL   string    `json:"html_url"`
	User      string    `json:"user,omitempty"`
	Head      string    `json:"head,omitempty"`
	Base      string    `json:"base,omitempty"`
	Body      string    `json:"body,omitempty"`
	Draft     bool      `json:"draft"`
	Merged    bool      `json:"merged"`
	Mergeable *bool     `json:"mergeable,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type reviewSummary struct {
	ID          int64     `json:"id"`
	State       string    `json:"state"`
	Body        string    `json:"body,omitempty"`
	HTMLURL     string    `json:"html_url"`
	SubmittedAt time.Time `json:"submitted_at,omitempty"`
	User        string    `json:"user,omitempty"`
}

type mergeSummary struct {
	SHA     string `json:"sha"`
	Merged  bool   `json:"merged"`
	Message string `json:"message"`
}

func registerPRTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("get_pr",
		mcp.WithDescription("Get detailed information about a pull request"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithNumber("number", mcp.Required(), mcp.Description("Pull request number")),
	), handleGetPR)

	s.MCPServer.AddTool(mcp.NewTool("create_pr",
		mcp.WithDescription("Create a new pull request"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("title", mcp.Required(), mcp.Description("Title of the pull request")),
		mcp.WithString("head", mcp.Required(), mcp.Description("Branch where your changes are implemented")),
		mcp.WithString("base", mcp.Required(), mcp.Description("Branch to merge changes into")),
		mcp.WithString("body", mcp.Description("Body content of the pull request")),
		mcp.WithBoolean("draft", mcp.Description("Open the pull request as a draft")),
	), handleCreatePR)

	s.MCPServer.AddTool(mcp.NewTool("review_pr",
		mcp.WithDescription("Submit a review on a pull request (approve, request changes, or comment)"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithNumber("number", mcp.Required(), mcp.Description("Pull request number")),
		mcp.WithString("event", mcp.Required(), mcp.Description("APPROVE, REQUEST_CHANGES or COMMENT")),
		mcp.WithString("body", mcp.Description("Optional review body (required for REQUEST_CHANGES)")),
	), handleReviewPR)

	s.MCPServer.AddTool(mcp.NewTool("merge_pr",
		mcp.WithDescription("Merge a pull request"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithNumber("number", mcp.Required(), mcp.Description("Pull request number")),
		mcp.WithString("commit_title", mcp.Description("Optional commit title")),
		mcp.WithString("commit_message", mcp.Description("Optional commit message body")),
		mcp.WithString("merge_method", mcp.Description("merge (default), squash, or rebase")),
		mcp.WithString("sha", mcp.Description("If set, the merge succeeds only if the PR head matches this SHA")),
	), handleMergePR)
}

func handleGetPR(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	pr, _, err := ghClient.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return githubError(err)
	}
	return jsonResult(prSummaryFrom(pr))
}

func handleCreatePR(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	head, err := req.RequireString("head")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	base, err := req.RequireString("base")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body := req.GetString("body", "")
	draft := req.GetBool("draft", false)

	newPR := &github.NewPullRequest{
		Title: &title,
		Head:  &head,
		Base:  &base,
		Body:  &body,
		Draft: &draft,
	}

	pr, _, err := ghClient.PullRequests.Create(ctx, owner, repo, newPR)
	if err != nil {
		return githubError(err)
	}
	return jsonResult(prSummaryFrom(pr))
}

func handleReviewPR(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	event, err := req.RequireString("event")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	event = strings.ToUpper(event)
	switch event {
	case "APPROVE", "REQUEST_CHANGES", "COMMENT":
	default:
		return mcp.NewToolResultError("event must be one of APPROVE, REQUEST_CHANGES, COMMENT"), nil
	}
	body := req.GetString("body", "")
	if event == "REQUEST_CHANGES" && body == "" {
		return mcp.NewToolResultError("body is required when event is REQUEST_CHANGES"), nil
	}

	rr := &github.PullRequestReviewRequest{Event: &event}
	if body != "" {
		rr.Body = &body
	}

	r, _, err := ghClient.PullRequests.CreateReview(ctx, owner, repo, number, rr)
	if err != nil {
		return githubError(err)
	}
	return jsonResult(reviewSummary{
		ID:          r.GetID(),
		State:       r.GetState(),
		Body:        r.GetBody(),
		HTMLURL:     r.GetHTMLURL(),
		SubmittedAt: r.GetSubmittedAt().Time,
		User:        r.GetUser().GetLogin(),
	})
}

func handleMergePR(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	method := strings.ToLower(req.GetString("merge_method", ""))
	switch method {
	case "", "merge", "squash", "rebase":
	default:
		return mcp.NewToolResultError("merge_method must be one of merge, squash, rebase"), nil
	}

	opts := &github.PullRequestOptions{
		CommitTitle: req.GetString("commit_title", ""),
		MergeMethod: method,
		SHA:         req.GetString("sha", ""),
	}
	commitMessage := req.GetString("commit_message", "")

	res, _, err := ghClient.PullRequests.Merge(ctx, owner, repo, number, commitMessage, opts)
	if err != nil {
		return githubError(err)
	}
	return jsonResult(mergeSummary{
		SHA:     res.GetSHA(),
		Merged:  res.GetMerged(),
		Message: res.GetMessage(),
	})
}

func prSummaryFrom(pr *github.PullRequest) prSummary {
	if pr == nil {
		return prSummary{}
	}
	out := prSummary{
		Number:    pr.GetNumber(),
		Title:     pr.GetTitle(),
		State:     pr.GetState(),
		HTMLURL:   pr.GetHTMLURL(),
		User:      pr.GetUser().GetLogin(),
		Body:      pr.GetBody(),
		Draft:     pr.GetDraft(),
		Merged:    pr.GetMerged(),
		Mergeable: pr.Mergeable,
		CreatedAt: pr.GetCreatedAt().Time,
		UpdatedAt: pr.GetUpdatedAt().Time,
	}
	if h := pr.GetHead(); h != nil {
		out.Head = fmt.Sprintf("%s:%s", h.GetRepo().GetFullName(), h.GetRef())
	}
	if b := pr.GetBase(); b != nil {
		out.Base = b.GetRef()
	}
	return out
}
