package main

import (
	"context"
	"errors"
	"time"

	"github.com/google/go-github/v60/github"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/mark3labs/mcp-go/mcp"
)

type repoSummary struct {
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Description   string    `json:"description,omitempty"`
	HTMLURL       string    `json:"html_url"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork"`
	Stars         int       `json:"stars"`
	Forks         int       `json:"forks"`
	OpenIssues    int       `json:"open_issues"`
	Language      string    `json:"language,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type branchSummary struct {
	Name      string `json:"name"`
	SHA       string `json:"sha"`
	Protected bool   `json:"protected"`
}

type tagSummary struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

type releaseSummary struct {
	ID          int64      `json:"id"`
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name,omitempty"`
	HTMLURL     string     `json:"html_url"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

type commitSummary struct {
	SHA       string    `json:"sha"`
	Message   string    `json:"message"`
	Author    string    `json:"author,omitempty"`
	AuthorAt  time.Time `json:"author_at,omitempty"`
	Committer string    `json:"committer,omitempty"`
	HTMLURL   string    `json:"html_url"`
}

type listResponse[T any] struct {
	Items     []T       `json:"items"`
	Count     int       `json:"count"`
	RateLimit *rateInfo `json:"_rate_limit,omitempty"`
}

func registerRepoTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("list_repos",
		mcp.WithDescription("List repositories for the authenticated user"),
		mcp.WithNumber("per_page", mcp.Description("Results per page (max 100, default 30)")),
		mcp.WithNumber("page", mcp.Description("Page number to retrieve (default 1)")),
	), handleListRepos)

	s.MCPServer.AddTool(mcp.NewTool("get_repo",
		mcp.WithDescription("Get detailed information about a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
	), handleGetRepo)

	s.MCPServer.AddTool(mcp.NewTool("list_branches",
		mcp.WithDescription("List branches of a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithBoolean("protected_only", mcp.Description("If true, return only protected branches")),
		mcp.WithNumber("per_page", mcp.Description("Results per page (max 100, default 30)")),
		mcp.WithNumber("page", mcp.Description("Page number to retrieve (default 1)")),
	), handleListBranches)

	s.MCPServer.AddTool(mcp.NewTool("list_tags",
		mcp.WithDescription("List tags of a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithNumber("per_page", mcp.Description("Results per page (max 100, default 30)")),
		mcp.WithNumber("page", mcp.Description("Page number to retrieve (default 1)")),
	), handleListTags)

	s.MCPServer.AddTool(mcp.NewTool("list_releases",
		mcp.WithDescription("List releases of a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithNumber("per_page", mcp.Description("Results per page (max 100, default 30)")),
		mcp.WithNumber("page", mcp.Description("Page number to retrieve (default 1)")),
	), handleListReleases)

	s.MCPServer.AddTool(mcp.NewTool("list_commits",
		mcp.WithDescription("List commits in a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("sha", mcp.Description("SHA or branch to start listing commits from")),
		mcp.WithString("path", mcp.Description("Only commits affecting this path")),
		mcp.WithString("author", mcp.Description("Filter by author login or email")),
		mcp.WithNumber("per_page", mcp.Description("Results per page (max 100, default 30)")),
		mcp.WithNumber("page", mcp.Description("Page number to retrieve (default 1)")),
	), handleListCommits)

	s.MCPServer.AddTool(mcp.NewTool("get_commit",
		mcp.WithDescription("Get a single commit by SHA or ref"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("sha", mcp.Required(), mcp.Description("Commit SHA, branch name, or tag")),
	), handleGetCommit)

	s.MCPServer.AddTool(mcp.NewTool("fork_repo",
		mcp.WithDescription("Fork a repository into the authenticated account or an organization"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the source repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the source repository")),
		mcp.WithString("organization", mcp.Description("Optional organization to fork into")),
		mcp.WithString("name", mcp.Description("Optional new name for the fork")),
		mcp.WithBoolean("default_branch_only", mcp.Description("If true, only fork the default branch")),
	), handleForkRepo)
}

func handleListRepos(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts := &github.RepositoryListOptions{ListOptions: paginationOpts(req)}
	repos, resp, err := ghClient.Repositories.List(ctx, "", opts)
	if err != nil {
		return githubError(err)
	}

	items := make([]repoSummary, 0, len(repos))
	for _, r := range repos {
		items = append(items, repoSummaryFrom(r))
	}
	return jsonResult(listResponse[repoSummary]{
		Items:     items,
		Count:     len(items),
		RateLimit: rateOf(resp),
	})
}

func handleGetRepo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	r, _, err := ghClient.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return githubError(err)
	}
	return jsonResult(repoSummaryFrom(r))
}

func handleListBranches(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &github.BranchListOptions{ListOptions: paginationOpts(req)}
	if req.GetBool("protected_only", false) {
		// BranchListOptions takes a *bool: nil means "all branches",
		// &true filters to protected, &false to unprotected. We only
		// expose the protected_only switch.
		t := true
		opts.Protected = &t
	}

	branches, resp, err := ghClient.Repositories.ListBranches(ctx, owner, repo, opts)
	if err != nil {
		return githubError(err)
	}

	items := make([]branchSummary, 0, len(branches))
	for _, b := range branches {
		items = append(items, branchSummary{
			Name:      b.GetName(),
			SHA:       b.GetCommit().GetSHA(),
			Protected: b.GetProtected(),
		})
	}
	return jsonResult(listResponse[branchSummary]{Items: items, Count: len(items), RateLimit: rateOf(resp)})
}

func handleListTags(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := paginationOpts(req)
	tags, resp, err := ghClient.Repositories.ListTags(ctx, owner, repo, &opts)
	if err != nil {
		return githubError(err)
	}

	items := make([]tagSummary, 0, len(tags))
	for _, t := range tags {
		items = append(items, tagSummary{
			Name: t.GetName(),
			SHA:  t.GetCommit().GetSHA(),
		})
	}
	return jsonResult(listResponse[tagSummary]{Items: items, Count: len(items), RateLimit: rateOf(resp)})
}

func handleListReleases(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := paginationOpts(req)
	releases, resp, err := ghClient.Repositories.ListReleases(ctx, owner, repo, &opts)
	if err != nil {
		return githubError(err)
	}

	items := make([]releaseSummary, 0, len(releases))
	for _, r := range releases {
		var pub *time.Time
		if !r.GetPublishedAt().IsZero() {
			t := r.GetPublishedAt().Time
			pub = &t
		}
		items = append(items, releaseSummary{
			ID:          r.GetID(),
			TagName:     r.GetTagName(),
			Name:        r.GetName(),
			HTMLURL:     r.GetHTMLURL(),
			Draft:       r.GetDraft(),
			Prerelease:  r.GetPrerelease(),
			PublishedAt: pub,
		})
	}
	return jsonResult(listResponse[releaseSummary]{Items: items, Count: len(items), RateLimit: rateOf(resp)})
}

func handleListCommits(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &github.CommitsListOptions{
		SHA:         req.GetString("sha", ""),
		Path:        req.GetString("path", ""),
		Author:      req.GetString("author", ""),
		ListOptions: paginationOpts(req),
	}

	commits, resp, err := ghClient.Repositories.ListCommits(ctx, owner, repo, opts)
	if err != nil {
		return githubError(err)
	}

	items := make([]commitSummary, 0, len(commits))
	for _, c := range commits {
		items = append(items, commitSummaryFrom(c))
	}
	return jsonResult(listResponse[commitSummary]{Items: items, Count: len(items), RateLimit: rateOf(resp)})
}

func handleGetCommit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	sha, err := req.RequireString("sha")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	c, _, err := ghClient.Repositories.GetCommit(ctx, owner, repo, sha, nil)
	if err != nil {
		return githubError(err)
	}
	return jsonResult(commitSummaryFrom(c))
}

func handleForkRepo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	opts := &github.RepositoryCreateForkOptions{
		Organization:      req.GetString("organization", ""),
		Name:              req.GetString("name", ""),
		DefaultBranchOnly: req.GetBool("default_branch_only", false),
	}

	r, _, err := ghClient.Repositories.CreateFork(ctx, owner, repo, opts)
	// CreateFork returns an *AcceptedError when the fork is queued. The
	// Repository payload is still populated, so we treat it as success and
	// surface the fact that the fork is async via a flag.
	pending := false
	if err != nil {
		var accepted *github.AcceptedError
		if !errors.As(err, &accepted) {
			return githubError(err)
		}
		pending = true
	}

	out := struct {
		repoSummary
		Pending bool `json:"pending"`
	}{
		repoSummary: repoSummaryFrom(r),
		Pending:     pending,
	}
	return jsonResult(out)
}

func repoSummaryFrom(r *github.Repository) repoSummary {
	if r == nil {
		return repoSummary{}
	}
	return repoSummary{
		Name:          r.GetName(),
		FullName:      r.GetFullName(),
		Description:   r.GetDescription(),
		HTMLURL:       r.GetHTMLURL(),
		DefaultBranch: r.GetDefaultBranch(),
		Private:       r.GetPrivate(),
		Fork:          r.GetFork(),
		Stars:         r.GetStargazersCount(),
		Forks:         r.GetForksCount(),
		OpenIssues:    r.GetOpenIssuesCount(),
		Language:      r.GetLanguage(),
		UpdatedAt:     r.GetUpdatedAt().Time,
	}
}

func commitSummaryFrom(c *github.RepositoryCommit) commitSummary {
	if c == nil {
		return commitSummary{}
	}
	out := commitSummary{
		SHA:     c.GetSHA(),
		HTMLURL: c.GetHTMLURL(),
	}
	if inner := c.GetCommit(); inner != nil {
		out.Message = inner.GetMessage()
		if a := inner.GetAuthor(); a != nil {
			out.Author = a.GetName()
			out.AuthorAt = a.GetDate().Time
		}
		if cm := inner.GetCommitter(); cm != nil {
			out.Committer = cm.GetName()
		}
	}
	return out
}
