package main

import (
	"context"

	"github.com/google/go-github/v60/github"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/mark3labs/mcp-go/mcp"
)

type codeHit struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	SHA        string `json:"sha"`
	HTMLURL    string `json:"html_url"`
	Repository string `json:"repository,omitempty"`
}

type searchResponse[T any] struct {
	Total             int       `json:"total"`
	IncompleteResults bool      `json:"incomplete_results"`
	Items             []T       `json:"items"`
	Count             int       `json:"count"`
	RateLimit         *rateInfo `json:"_rate_limit,omitempty"`
}

func registerSearchTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("search_code",
		mcp.WithDescription("Search code on GitHub. Use the same syntax as the web UI (e.g. \"language:go addr in:file repo:owner/name\")"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query string")),
		mcp.WithString("sort", mcp.Description("Sort field: indexed (default best match)")),
		mcp.WithString("order", mcp.Description("Sort order: asc or desc (default desc)")),
		mcp.WithNumber("per_page", mcp.Description("Results per page (max 100, default 30)")),
		mcp.WithNumber("page", mcp.Description("Page number to retrieve (default 1)")),
	), handleSearchCode)

	s.MCPServer.AddTool(mcp.NewTool("search_issues",
		mcp.WithDescription("Search issues and pull requests across GitHub"),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query string")),
		mcp.WithString("sort", mcp.Description("Sort field: comments, created, updated")),
		mcp.WithString("order", mcp.Description("Sort order: asc or desc (default desc)")),
		mcp.WithNumber("per_page", mcp.Description("Results per page (max 100, default 30)")),
		mcp.WithNumber("page", mcp.Description("Page number to retrieve (default 1)")),
	), handleSearchIssues)
}

func searchOptsFrom(req mcp.CallToolRequest) *github.SearchOptions {
	return &github.SearchOptions{
		Sort:        req.GetString("sort", ""),
		Order:       req.GetString("order", ""),
		ListOptions: paginationOpts(req),
	}
}

func handleSearchCode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	res, resp, err := ghClient.Search.Code(ctx, query, searchOptsFrom(req))
	if err != nil {
		return githubError(err)
	}

	items := make([]codeHit, 0, len(res.CodeResults))
	for _, r := range res.CodeResults {
		items = append(items, codeHit{
			Name:       r.GetName(),
			Path:       r.GetPath(),
			SHA:        r.GetSHA(),
			HTMLURL:    r.GetHTMLURL(),
			Repository: r.GetRepository().GetFullName(),
		})
	}
	return jsonResult(searchResponse[codeHit]{
		Total:             res.GetTotal(),
		IncompleteResults: res.GetIncompleteResults(),
		Items:             items,
		Count:             len(items),
		RateLimit:         rateOf(resp),
	})
}

func handleSearchIssues(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	res, resp, err := ghClient.Search.Issues(ctx, query, searchOptsFrom(req))
	if err != nil {
		return githubError(err)
	}

	items := make([]issueSummary, 0, len(res.Issues))
	for _, i := range res.Issues {
		items = append(items, issueSummaryFrom(i))
	}
	return jsonResult(searchResponse[issueSummary]{
		Total:             res.GetTotal(),
		IncompleteResults: res.GetIncompleteResults(),
		Items:             items,
		Count:             len(items),
		RateLimit:         rateOf(resp),
	})
}
