package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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

	server := core.NewDroidServer("mcp-filesystem", "1.0.0")

	// Register tools
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

func registerTools(s *core.DroidServer) {
	// read_file
	readFileTool := mcp.NewTool("read_file",
		mcp.WithDescription("Read the contents of a file"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file relative to root")),
	)
	s.MCPServer.AddTool(readFileTool, handleReadFile)

	// write_file
	writeFileTool := mcp.NewTool("write_file",
		mcp.WithDescription("Write content to a file"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file relative to root")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Content to write")),
	)
	s.MCPServer.AddTool(writeFileTool, handleWriteFile)

	// list_directory
	listDirTool := mcp.NewTool("list_directory",
		mcp.WithDescription("List contents of a directory"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the directory relative to root")),
	)
	s.MCPServer.AddTool(listDirTool, handleListDirectory)

	// search_files
	searchFilesTool := mcp.NewTool("search_files",
		mcp.WithDescription("Search for files by pattern"),
		mcp.WithString("root", mcp.Description("Directory to start search from (relative to root)")),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Glob pattern to search for")),
	)
	s.MCPServer.AddTool(searchFilesTool, handleSearchFiles)

	// delete_file
	deleteFileTool := mcp.NewTool("delete_file",
		mcp.WithDescription("Delete a file or empty directory"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file/dir relative to root")),
	)
	s.MCPServer.AddTool(deleteFileTool, handleDeleteFile)

	// move_file
	moveFileTool := mcp.NewTool("move_file",
		mcp.WithDescription("Move or rename a file/directory"),
		mcp.WithString("source", mcp.Required(), mcp.Description("Source path relative to root")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination path relative to root")),
	)
	s.MCPServer.AddTool(moveFileTool, handleMoveFile)
}

func securePath(relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		// Even if Joined it's better to be explicit about rejecting absolute paths
		// to avoid confusion about what the "root" is.
		return "", fmt.Errorf("absolute paths are not allowed: %s", relPath)
	}
	absRoot, err := filepath.Abs(cfg.Root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absRoot, relPath)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(absTarget, absRoot) {
		return "", fmt.Errorf("access denied: path escapes root")
	}
	return absTarget, nil
}

func handleReadFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := req.RequireString("path")
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(string(content)), nil
}

func handleWriteFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := req.RequireString("path")
	content, _ := req.RequireString("content")
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully wrote to %s", path)), nil
}

func handleListDirectory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := req.RequireString("path")
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var builder strings.Builder
	for _, entry := range entries {
		info, _ := entry.Info()
		typeStr := "F"
		if entry.IsDir() {
			typeStr = "D"
		}
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		builder.WriteString(fmt.Sprintf("[%s] %-20s %d bytes\n", typeStr, entry.Name(), size))
	}

	return mcp.NewToolResultText(builder.String()), nil
}

func handleSearchFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	searchRootRel := req.GetString("root", ".")
	pattern, _ := req.RequireString("pattern")

	searchRoot, err := securePath(searchRootRel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var matches []string
	err = filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // ignore errors during walk
		}
		rel, _ := filepath.Rel(searchRoot, path)
		matched, _ := filepath.Match(pattern, d.Name())
		if matched {
			matches = append(matches, rel)
		}
		return nil
	})

	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if len(matches) == 0 {
		return mcp.NewToolResultText("No matches found"), nil
	}

	return mcp.NewToolResultText(strings.Join(matches, "\n")), nil
}

func handleDeleteFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, _ := req.RequireString("path")
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := os.Remove(fullPath); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully deleted %s", path)), nil
}

func handleMoveFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	src, _ := req.RequireString("source")
	dst, _ := req.RequireString("destination")

	fullSrc, err := securePath(src)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	fullDst, err := securePath(dst)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := os.Rename(fullSrc, fullDst); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully moved %s to %s", src, dst)), nil
}
