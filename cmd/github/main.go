// Command github provides an MCP server for interacting with GitHub.
// It uses OAuth2 for authentication and the official google/go-github client.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

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

// tokenStartupTimeout caps the Users.Get(ctx, "") health check so a
// misconfigured network does not block the server from starting forever.
const tokenStartupTimeout = 10 * time.Second

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	token, source := resolveGitHubToken()
	if token == "" {
		logger.Fatal("No GitHub token found: set GITHUB_TOKEN, GITHUB_APP_TOKEN or GITHUB_FINE_GRAINED_TOKEN", nil)
	}

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	oauthClient := oauth2.NewClient(ctx, ts)
	// Wrap the OAuth2 transport with our retry layer. The OAuth2 client is the
	// outermost layer (it adds the Authorization header), so the retry sits
	// between it and the default transport.
	oauthClient.Transport = newRetryTransport(oauthClient.Transport)

	ghClient = github.NewClient(oauthClient)

	if err := validateToken(ctx, ghClient, source); err != nil {
		logger.Fatal("GitHub token validation failed", err, "source", source)
	}

	server := core.NewDroidServer("mcp-github", "1.0.0")
	server.APIKey = config.ResolveAPIKey("github")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

// resolveGitHubToken looks up the token from the supported environment
// variables in priority order. It returns the value plus the env name it came
// from (for logging) so the operator can see which credential is in use.
func resolveGitHubToken() (token, source string) {
	for _, name := range []string{"GITHUB_TOKEN", "GITHUB_APP_TOKEN", "GITHUB_FINE_GRAINED_TOKEN"} {
		if v := os.Getenv(name); v != "" {
			return v, name
		}
	}
	return "", ""
}

// validateToken probes the GitHub API with the configured credential so we
// fail fast at startup instead of returning misleading 401s for every tool
// call. Users.Get(ctx, "") returns the authenticated user.
func validateToken(parent context.Context, c *github.Client, source string) error {
	ctx, cancel := context.WithTimeout(parent, tokenStartupTimeout)
	defer cancel()
	user, _, err := c.Users.Get(ctx, "")
	if err != nil {
		return err
	}
	logger.Info("GitHub token validated", "source", source, "login", user.GetLogin())
	return nil
}

func registerTools(s *core.DroidServer) {
	registerRepoTools(s)
	registerIssueTools(s)
	registerPRTools(s)
	registerFileTools(s)
	registerSearchTools(s)
}

// jsonResult marshals v as indented JSON and returns it as a tool result. If
// marshaling fails (which would indicate a programming error: an unexported
// field tagged for JSON, a cyclic structure, etc.) we fall back to an error
// result so the caller sees a useful message instead of an empty payload.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// paginationOpts reads optional per_page/page parameters and returns a
// github.ListOptions ready to embed in any List* call.
func paginationOpts(req mcp.CallToolRequest) github.ListOptions {
	return github.ListOptions{
		Page:    req.GetInt("page", 0),
		PerPage: req.GetInt("per_page", 0),
	}
}

// githubError converts an error returned from a go-github call into a tool
// result. Rate-limit and abuse-rate-limit errors are surfaced with the reset
// time / retry hint so callers (or upstream agents) can back off intelligently
// instead of guessing.
func githubError(err error) (*mcp.CallToolResult, error) {
	if err == nil {
		return nil, nil
	}
	var rl *github.RateLimitError
	if errors.As(err, &rl) {
		var b strings.Builder
		fmt.Fprintf(&b, "GitHub rate limit hit: %s", rl.Message)
		fmt.Fprintf(&b, " (limit=%d remaining=%d resets=%s)",
			rl.Rate.Limit, rl.Rate.Remaining, rl.Rate.Reset.Format(time.RFC3339))
		return mcp.NewToolResultError(b.String()), nil
	}
	var abuse *github.AbuseRateLimitError
	if errors.As(err, &abuse) {
		msg := fmt.Sprintf("GitHub secondary rate limit: %s", abuse.Message)
		if abuse.RetryAfter != nil {
			msg += fmt.Sprintf(" (retry after %s)", abuse.RetryAfter.Round(time.Second))
		}
		return mcp.NewToolResultError(msg), nil
	}
	return mcp.NewToolResultError(err.Error()), nil
}

// withRateLimit reads X-RateLimit-* headers off a successful response so the
// agent can decide whether to keep going. It is purely informational; the
// retry transport handles the wire-level cases. The shape is exposed in JSON
// payloads under the "_rate_limit" key.
type rateInfo struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Reset     time.Time `json:"reset"`
}

func rateOf(resp *github.Response) *rateInfo {
	if resp == nil {
		return nil
	}
	return &rateInfo{
		Limit:     resp.Rate.Limit,
		Remaining: resp.Rate.Remaining,
		Reset:     resp.Rate.Reset.Time,
	}
}

// notFound returns true if err is an *http.StatusNotFound from GitHub.
func notFound(err error) bool {
	var er *github.ErrorResponse
	return errors.As(err, &er) && er.Response != nil && er.Response.StatusCode == http.StatusNotFound
}
