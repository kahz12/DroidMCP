package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/go-github/v60/github"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/mark3labs/mcp-go/mcp"
)

type fileResult struct {
	Path     string `json:"path"`
	SHA      string `json:"sha,omitempty"`
	Size     int    `json:"size"`
	Encoding string `json:"encoding,omitempty"`
	Content  string `json:"content"`
	HTMLURL  string `json:"html_url,omitempty"`
}

type commitFileResult struct {
	Path    string `json:"path"`
	SHA     string `json:"sha,omitempty"`
	Created bool   `json:"created"`
	HTMLURL string `json:"html_url,omitempty"`
}

func registerFileTools(s *core.DroidServer) {
	s.MCPServer.AddTool(mcp.NewTool("get_file",
		mcp.WithDescription("Get the contents of a file from a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file in the repository")),
		mcp.WithString("ref", mcp.Description("Commit/branch/tag. Default: the repository's default branch")),
	), handleGetFile)

	s.MCPServer.AddTool(mcp.NewTool("commit_file",
		mcp.WithDescription("Create or update a file in a repository"),
		mcp.WithString("owner", mcp.Required(), mcp.Description("Owner of the repository")),
		mcp.WithString("repo", mcp.Required(), mcp.Description("Name of the repository")),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file in the repository")),
		mcp.WithString("content", mcp.Required(), mcp.Description("New content of the file")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Commit message")),
		mcp.WithString("branch", mcp.Description("Branch name. Default: the repository's default branch")),
	), handleCommitFile)
}

func handleGetFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	ref := req.GetString("ref", "")

	fileContent, _, _, err := ghClient.Repositories.GetContents(ctx, owner, repo, path, &github.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		return githubError(err)
	}
	if fileContent == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Path %q is a directory, not a file", path)), nil
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(fileResult{
		Path:     fileContent.GetPath(),
		SHA:      fileContent.GetSHA(),
		Size:     fileContent.GetSize(),
		Encoding: fileContent.GetEncoding(),
		Content:  content,
		HTMLURL:  fileContent.GetHTMLURL(),
	})
}

func handleCommitFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	owner, err := req.RequireString("owner")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	repo, err := req.RequireString("repo")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	message, err := req.RequireString("message")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	branch := req.GetString("branch", "")

	// Look up the existing SHA so we can decide whether this is a create or
	// an update. SHA is mandatory for updates; absence on a create call is
	// fine. We must distinguish 404 (file does not exist) from any other
	// error so we don't silently turn a 5xx into a "create" call.
	getOpts := &github.RepositoryContentGetOptions{Ref: branch}
	fileContent, dirContent, _, err := ghClient.Repositories.GetContents(ctx, owner, repo, path, getOpts)

	var sha *string
	created := true
	if err != nil {
		var errResp *github.ErrorResponse
		if !errors.As(err, &errResp) || errResp.Response == nil || !notFound(err) {
			return mcp.NewToolResultError(fmt.Sprintf("Error checking existing file: %v", err)), nil
		}
	} else if dirContent != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Path %q is a directory, not a file", path)), nil
	} else if fileContent != nil {
		sha = fileContent.SHA
		created = false
	}

	opts := &github.RepositoryContentFileOptions{
		Message: github.String(message),
		Content: []byte(content),
		SHA:     sha,
	}
	if branch != "" {
		opts.Branch = github.String(branch)
	}

	resp, _, err := ghClient.Repositories.CreateFile(ctx, owner, repo, path, opts)
	if err != nil {
		return githubError(err)
	}
	out := commitFileResult{
		Path:    path,
		Created: created,
	}
	if resp != nil {
		out.SHA = resp.GetSHA()
		out.HTMLURL = resp.GetHTMLURL()
	}
	return jsonResult(out)
}
