// Command scraper provides an MCP server for lightweight web scraping.
// All requests go through an SSRF-safe HTTP client (security.go) and an
// optional in-memory LRU (cache.go). Handlers parse arguments, call fetch(),
// and run the response through goquery for the structured tools.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/kahz12/droidmcp/internal/buildinfo"
	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/mark3labs/mcp-go/mcp"
)

var cfg *config.Config

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	server := core.NewDroidServer("mcp-scraper", buildinfo.Version)
	server.APIKey = config.ResolveAPIKey("scraper")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	// Common option set: headers, user_agent, timeout_seconds, no_cache.
	commonOpts := []mcp.ToolOption{
		mcp.WithObject("headers", mcp.Description("Optional map of request header name -> value")),
		mcp.WithString("user_agent", mcp.Description("Override the User-Agent header for this request")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-request timeout. Default 20 seconds, max 60.")),
		mcp.WithBoolean("no_cache", mcp.Description("If true, bypass the in-memory response cache for this call")),
		mcp.WithString("wait_selector", mcp.Description("Retry until this CSS selector matches (server-rendered + lazy data)")),
		mcp.WithNumber("wait_attempts", mcp.Description("Maximum retries when wait_selector is set. Default 3, max 10.")),
		mcp.WithNumber("wait_interval_ms", mcp.Description("Delay between retries when wait_selector is set. Default 1000ms.")),
	}

	addTool := func(t mcp.Tool, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
		s.MCPServer.AddTool(t, h)
	}

	addTool(mcp.NewTool("fetch_page",
		append([]mcp.ToolOption{
			mcp.WithDescription("Fetch the HTML content of a URL. Returns JSON with status, headers, and body."),
			mcp.WithString("url", mcp.Required(), mcp.Description("URL to fetch (http or https only)")),
		}, commonOpts...)...,
	), handleFetchPage)

	addTool(mcp.NewTool("extract_text",
		append([]mcp.ToolOption{
			mcp.WithDescription("Extract clean text from a URL. Optional CSS selector limits the extraction to a region."),
			mcp.WithString("url", mcp.Required(), mcp.Description("URL to extract from")),
			mcp.WithString("selector", mcp.Description("Optional CSS selector. Default: extract from <body>")),
		}, commonOpts...)...,
	), handleExtractText)

	addTool(mcp.NewTool("extract_links",
		append([]mcp.ToolOption{
			mcp.WithDescription("Extract all links from a URL. Returns absolute URLs with anchor text and rel."),
			mcp.WithString("url", mcp.Required(), mcp.Description("URL to extract from")),
			mcp.WithString("selector", mcp.Description("Optional CSS selector to limit the search. Default: a[href]")),
		}, commonOpts...)...,
	), handleExtractLinks)

	addTool(mcp.NewTool("extract_table",
		append([]mcp.ToolOption{
			mcp.WithDescription("Extract HTML tables from a URL as structured JSON."),
			mcp.WithString("url", mcp.Required(), mcp.Description("URL to extract from")),
			mcp.WithString("selector", mcp.Description("Optional CSS selector for the table. Default: table")),
		}, commonOpts...)...,
	), handleExtractTable)

	addTool(mcp.NewTool("extract_metadata",
		append([]mcp.ToolOption{
			mcp.WithDescription("Extract page metadata: title, description, canonical, og:*, twitter:*."),
			mcp.WithString("url", mcp.Required(), mcp.Description("URL to extract from")),
		}, commonOpts...)...,
	), handleExtractMetadata)

	addTool(mcp.NewTool("search_in_page",
		append([]mcp.ToolOption{
			mcp.WithDescription("Search for text or a pattern in the visible text of a URL. Returns matches with surrounding context."),
			mcp.WithString("url", mcp.Required(), mcp.Description("URL to search in")),
			mcp.WithString("query", mcp.Required(), mcp.Description("Text (or Go regular expression when regex=true) to search for")),
			mcp.WithBoolean("regex", mcp.Description("Treat query as a Go regular expression. Default: literal text")),
			mcp.WithBoolean("case_sensitive", mcp.Description("Match case exactly. Default: case-insensitive")),
			mcp.WithString("selector", mcp.Description("Optional CSS selector to limit the search region. Default: <body>")),
			mcp.WithNumber("max_results", mcp.Description("Maximum matches to return. Default 20, max 100.")),
			mcp.WithNumber("context_chars", mcp.Description("Context around each match, per side. Default 80, max 500.")),
		}, commonOpts...)...,
	), handleSearchInPage)
}

// fetchResultJSON is the wire format for fetch_page (and the source of the
// _meta block that the structured tools embed).
type fetchResultJSON struct {
	URL       string            `json:"url"`
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body"`
	BodyBytes int               `json:"body_bytes"`
	FetchedAt time.Time         `json:"fetched_at"`
	FromCache bool              `json:"from_cache"`
}

type linksResult struct {
	URL       string     `json:"url"`
	Status    int        `json:"status"`
	Count     int        `json:"count"`
	Items     []linkItem `json:"items"`
	FromCache bool       `json:"from_cache"`
}

type linkItem struct {
	Href  string `json:"href"`
	Text  string `json:"text,omitempty"`
	Rel   string `json:"rel,omitempty"`
	Title string `json:"title,omitempty"`
}

type textResult struct {
	URL       string `json:"url"`
	Status    int    `json:"status"`
	Selector  string `json:"selector,omitempty"`
	Text      string `json:"text"`
	FromCache bool   `json:"from_cache"`
}

type tableResult struct {
	URL       string                `json:"url"`
	Status    int                   `json:"status"`
	Count     int                   `json:"count"`
	Tables    [][]map[string]string `json:"tables"`
	FromCache bool                  `json:"from_cache"`
}

type searchResult struct {
	URL       string        `json:"url"`
	Status    int           `json:"status"`
	Query     string        `json:"query"`
	Regex     bool          `json:"regex,omitempty"`
	Selector  string        `json:"selector,omitempty"`
	Count     int           `json:"count"`
	Truncated bool          `json:"truncated,omitempty"`
	Matches   []searchMatch `json:"matches"`
	FromCache bool          `json:"from_cache"`
}

type searchMatch struct {
	Match   string `json:"match"`
	Context string `json:"context"`
	Offset  int    `json:"offset"`
}

type metadataResult struct {
	URL         string            `json:"url"`
	Status      int               `json:"status"`
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Canonical   string            `json:"canonical,omitempty"`
	OpenGraph   map[string]string `json:"open_graph,omitempty"`
	Twitter     map[string]string `json:"twitter,omitempty"`
	FromCache   bool              `json:"from_cache"`
}

func handleFetchPage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts, err := parseFetchOptions(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := fetch(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return jsonResult(fetchResultJSON{
		URL:       resp.URL,
		Status:    resp.Status,
		Headers:   flattenHeaders(resp.Header),
		Body:      string(resp.Body),
		BodyBytes: len(resp.Body),
		FetchedAt: resp.FetchedAt,
		FromCache: resp.FromCache,
	})
}

func handleExtractText(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts, err := parseFetchOptions(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	selector := strings.TrimSpace(req.GetString("selector", ""))
	resp, err := fetch(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	doc, derr := goquery.NewDocumentFromReader(strings.NewReader(string(resp.Body)))
	if derr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse HTML: %v", derr)), nil
	}
	doc.Find("script, style, iframe, noscript").Remove()

	var text string
	if selector == "" {
		text = strings.TrimSpace(doc.Find("body").Text())
		if text == "" {
			// Pages without an explicit <body> still have a Text() value at root.
			text = strings.TrimSpace(doc.Text())
		}
	} else {
		sel := doc.Find(selector)
		if sel.Length() == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("selector %q matched nothing", selector)), nil
		}
		text = strings.TrimSpace(sel.Text())
	}
	text = strings.Join(strings.Fields(text), " ")

	return jsonResult(textResult{
		URL:       resp.URL,
		Status:    resp.Status,
		Selector:  selector,
		Text:      text,
		FromCache: resp.FromCache,
	})
}

func handleExtractLinks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts, err := parseFetchOptions(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	selector := req.GetString("selector", "a[href]")
	resp, err := fetch(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	doc, derr := goquery.NewDocumentFromReader(strings.NewReader(string(resp.Body)))
	if derr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse HTML: %v", derr)), nil
	}

	var items []linkItem
	doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok || strings.TrimSpace(href) == "" {
			return
		}
		abs := absoluteURL(resp.URL, href)
		items = append(items, linkItem{
			Href:  abs,
			Text:  strings.TrimSpace(s.Text()),
			Rel:   s.AttrOr("rel", ""),
			Title: s.AttrOr("title", ""),
		})
	})

	return jsonResult(linksResult{
		URL:       resp.URL,
		Status:    resp.Status,
		Count:     len(items),
		Items:     items,
		FromCache: resp.FromCache,
	})
}

func handleExtractTable(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts, err := parseFetchOptions(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	selector := req.GetString("selector", "table")
	resp, err := fetch(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	doc, derr := goquery.NewDocumentFromReader(strings.NewReader(string(resp.Body)))
	if derr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse HTML: %v", derr)), nil
	}

	var tables [][]map[string]string
	doc.Find(selector).Each(func(i int, tableHtml *goquery.Selection) {
		var table []map[string]string
		var headers []string

		tableHtml.Find("thead tr").First().Find("th, td").Each(func(_ int, cellHtml *goquery.Selection) {
			headers = append(headers, strings.TrimSpace(cellHtml.Text()))
		})
		hadThead := len(headers) > 0

		rows := tableHtml.Find("tbody tr")
		if rows.Length() == 0 {
			rows = tableHtml.Find("tr")
			if hadThead {
				rows = rows.NotSelection(tableHtml.Find("thead tr"))
			}
		}

		rows.Each(func(j int, rowHtml *goquery.Selection) {
			if !hadThead && j == 0 {
				rowHtml.Find("th, td").Each(func(_ int, cellHtml *goquery.Selection) {
					headers = append(headers, strings.TrimSpace(cellHtml.Text()))
				})
				return
			}
			rowData := make(map[string]string)
			rowHtml.Find("td").Each(func(k int, cellHtml *goquery.Selection) {
				header := fmt.Sprintf("col%d", k)
				if k < len(headers) {
					header = headers[k]
				}
				rowData[header] = strings.TrimSpace(cellHtml.Text())
			})
			if len(rowData) > 0 {
				table = append(table, rowData)
			}
		})
		if len(table) > 0 {
			tables = append(tables, table)
		}
	})

	return jsonResult(tableResult{
		URL:       resp.URL,
		Status:    resp.Status,
		Count:     len(tables),
		Tables:    tables,
		FromCache: resp.FromCache,
	})
}

func handleExtractMetadata(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts, err := parseFetchOptions(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resp, err := fetch(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	doc, derr := goquery.NewDocumentFromReader(strings.NewReader(string(resp.Body)))
	if derr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse HTML: %v", derr)), nil
	}

	out := metadataResult{
		URL:       resp.URL,
		Status:    resp.Status,
		Title:     strings.TrimSpace(doc.Find("title").First().Text()),
		FromCache: resp.FromCache,
	}
	if v, ok := doc.Find(`meta[name="description"]`).Attr("content"); ok {
		out.Description = strings.TrimSpace(v)
	}
	if v, ok := doc.Find(`link[rel="canonical"]`).Attr("href"); ok {
		out.Canonical = absoluteURL(resp.URL, strings.TrimSpace(v))
	}

	og := map[string]string{}
	doc.Find(`meta[property]`).Each(func(_ int, s *goquery.Selection) {
		prop, _ := s.Attr("property")
		prop = strings.ToLower(strings.TrimSpace(prop))
		if !strings.HasPrefix(prop, "og:") {
			return
		}
		content, _ := s.Attr("content")
		og[strings.TrimPrefix(prop, "og:")] = strings.TrimSpace(content)
	})
	if len(og) > 0 {
		out.OpenGraph = og
	}

	tw := map[string]string{}
	doc.Find(`meta[name]`).Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		name = strings.ToLower(strings.TrimSpace(name))
		if !strings.HasPrefix(name, "twitter:") {
			return
		}
		content, _ := s.Attr("content")
		tw[strings.TrimPrefix(name, "twitter:")] = strings.TrimSpace(content)
	})
	if len(tw) > 0 {
		out.Twitter = tw
	}

	return jsonResult(out)
}

func handleSearchInPage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	opts, err := parseFetchOptions(req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	isRegex := req.GetBool("regex", false)
	selector := strings.TrimSpace(req.GetString("selector", ""))

	maxResults := req.GetInt("max_results", 20)
	if maxResults <= 0 {
		maxResults = 20
	} else if maxResults > 100 {
		maxResults = 100
	}
	contextChars := req.GetInt("context_chars", 80)
	if contextChars < 0 {
		contextChars = 80
	} else if contextChars > 500 {
		contextChars = 500
	}

	pattern := query
	if !isRegex {
		pattern = regexp.QuoteMeta(query)
	}
	if !req.GetBool("case_sensitive", false) {
		pattern = "(?i)" + pattern
	}
	re, cerr := regexp.Compile(pattern)
	if cerr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid pattern: %v", cerr)), nil
	}
	if re.MatchString("") {
		return mcp.NewToolResultError("query must not match the empty string"), nil
	}

	resp, err := fetch(ctx, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	doc, derr := goquery.NewDocumentFromReader(strings.NewReader(string(resp.Body)))
	if derr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to parse HTML: %v", derr)), nil
	}
	doc.Find("script, style, iframe, noscript").Remove()

	var text string
	if selector == "" {
		text = strings.TrimSpace(doc.Find("body").Text())
		if text == "" {
			text = strings.TrimSpace(doc.Text())
		}
	} else {
		sel := doc.Find(selector)
		if sel.Length() == 0 {
			return mcp.NewToolResultError(fmt.Sprintf("selector %q matched nothing", selector)), nil
		}
		text = strings.TrimSpace(sel.Text())
	}
	text = strings.Join(strings.Fields(text), " ")

	locs := re.FindAllStringIndex(text, -1)
	out := searchResult{
		URL:       resp.URL,
		Status:    resp.Status,
		Query:     query,
		Regex:     isRegex,
		Selector:  selector,
		Count:     len(locs),
		Truncated: len(locs) > maxResults,
		Matches:   []searchMatch{},
		FromCache: resp.FromCache,
	}
	if len(locs) > maxResults {
		locs = locs[:maxResults]
	}
	for _, loc := range locs {
		start := loc[0] - contextChars
		if start < 0 {
			start = 0
		}
		end := loc[1] + contextChars
		if end > len(text) {
			end = len(text)
		}
		// Snap to rune boundaries so context never splits a UTF-8 sequence.
		for start > 0 && !utf8.RuneStart(text[start]) {
			start--
		}
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end++
		}
		out.Matches = append(out.Matches, searchMatch{
			Match:   text[loc[0]:loc[1]],
			Context: text[start:end],
			Offset:  loc[0],
		})
	}
	return jsonResult(out)
}

// parseFetchOptions extracts the shared fetch knobs (headers, user_agent,
// timeout, wait_selector, no_cache) from an MCP request and returns
// fetchOptions ready to hand to fetch().
func parseFetchOptions(req mcp.CallToolRequest) (fetchOptions, error) {
	rawURL, err := req.RequireString("url")
	if err != nil {
		return fetchOptions{}, err
	}
	opts := fetchOptions{
		URL:          rawURL,
		UserAgent:    req.GetString("user_agent", ""),
		Headers:      stringMapArg(req, "headers"),
		NoCache:      req.GetBool("no_cache", false),
		WaitSelector: req.GetString("wait_selector", ""),
		WaitAttempts: req.GetInt("wait_attempts", 0),
	}
	if t := req.GetInt("timeout_seconds", 0); t > 0 {
		if t > 60 {
			t = 60
		}
		opts.Timeout = time.Duration(t) * time.Second
	}
	if ms := req.GetInt("wait_interval_ms", 0); ms > 0 {
		opts.WaitInterval = time.Duration(ms) * time.Millisecond
	}
	return opts, nil
}

// stringMapArg pulls a string->string map out of a JSON-decoded object arg,
// dropping non-string values silently. This is what mcp-go gives us for
// WithObject; it does not have a typed getter.
func stringMapArg(req mcp.CallToolRequest, name string) map[string]string {
	args := req.GetArguments()
	v, ok := args[name]
	if !ok || v == nil {
		return nil
	}
	raw, ok := v.(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, val := range raw {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// flattenHeaders converts an http.Header (multi-value) to the simpler
// string->string map our JSON contract uses. Only the first value per key is
// kept; this matches what most callers need and keeps the payload small.
func flattenHeaders(h map[string][]string) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// absoluteURL resolves a possibly-relative href against base. If anything goes
// wrong (unparseable base or href) we return the raw href so the caller still
// sees something useful.
func absoluteURL(base, href string) string {
	bu, err := url.Parse(base)
	if err != nil {
		return href
	}
	hu, err := url.Parse(href)
	if err != nil {
		return href
	}
	return bu.ResolveReference(hu).String()
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal response: %v", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}
