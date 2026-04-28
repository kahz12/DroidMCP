// Command scraper provides an MCP server for lightweight web scraping.
// It uses Colly for crawling and GoQuery for DOM traversal.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
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

	server := core.NewDroidServer("mcp-scraper", "1.0.0")
	server.APIKey = config.ResolveAPIKey("scraper")
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	// fetch_page: Returns raw HTML. Useful for LLMs to do their own parsing.
	fetchPageTool := mcp.NewTool("fetch_page",
		mcp.WithDescription("Fetch the HTML content of a URL"),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL to fetch")),
	)
	s.MCPServer.AddTool(fetchPageTool, handleFetchPage)

	// extract_text: Returns cleaned, human-readable text. Strips script/style tags.
	extractTextTool := mcp.NewTool("extract_text",
		mcp.WithDescription("Extract clean text from a URL"),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL to extract from")),
	)
	s.MCPServer.AddTool(extractTextTool, handleExtractText)

	// extract_links: Returns all absolute URLs found in 'a' tags.
	extractLinksTool := mcp.NewTool("extract_links",
		mcp.WithDescription("Extract all links from a URL"),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL to extract from")),
	)
	s.MCPServer.AddTool(extractLinksTool, handleExtractLinks)

	// extract_table: Converts HTML tables to a structured JSON format.
	extractTableTool := mcp.NewTool("extract_table",
		mcp.WithDescription("Extract HTML tables from a URL as JSON"),
		mcp.WithString("url", mcp.Required(), mcp.Description("URL to extract from")),
		mcp.WithString("selector", mcp.Description("Optional CSS selector for the table. Default: table")),
	)
	s.MCPServer.AddTool(extractTableTool, handleExtractTable)
}

func handleFetchPage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	url, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	c := colly.NewCollector()
	var html string

	c.OnResponse(func(r *colly.Response) {
		html = string(r.Body)
	})

	err = c.Visit(url)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(html), nil
}

func handleExtractText(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	url, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	c := colly.NewCollector()
	var text string

	c.OnResponse(func(r *colly.Response) {
		doc, derr := goquery.NewDocumentFromReader(strings.NewReader(string(r.Body)))
		if derr == nil {
			// DOM manipulation to remove noise before extracting text.
			doc.Find("script, style, iframe, noscript").Remove()
			text = strings.TrimSpace(doc.Text())
			// Collapse multiple spaces/newlines for cleaner LLM input.
			text = strings.Join(strings.Fields(text), " ")
		}
	})

	err = c.Visit(url)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(text), nil
}

func handleExtractLinks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	url, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	c := colly.NewCollector()
	var links []string

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		// e.Request.AbsoluteURL ensures we get the full URL even for relative links.
		links = append(links, e.Request.AbsoluteURL(e.Attr("href")))
	})

	err = c.Visit(url)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(strings.Join(links, "\n")), nil
}

func handleExtractTable(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	url, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	selector := req.GetString("selector", "table")
	c := colly.NewCollector()
	var tables [][]map[string]string

	c.OnResponse(func(r *colly.Response) {
		doc, derr := goquery.NewDocumentFromReader(strings.NewReader(string(r.Body)))
		if derr != nil {
			return
		}

		doc.Find(selector).Each(func(i int, tableHtml *goquery.Selection) {
			var table []map[string]string
			var headers []string

			tableHtml.Find("tr").Each(func(j int, rowHtml *goquery.Selection) {
				// Detect headers in the first row.
				if j == 0 {
					rowHtml.Find("th, td").Each(func(k int, cellHtml *goquery.Selection) {
						headers = append(headers, strings.TrimSpace(cellHtml.Text()))
					})
				} else {
					// Map row data to header keys.
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
				}
			})
			if len(table) > 0 {
				tables = append(tables, table)
			}
		})
	})

	err = c.Visit(url)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	jsonData, _ := json.MarshalIndent(tables, "", "  ")
	return mcp.NewToolResultText(string(jsonData)), nil
}
