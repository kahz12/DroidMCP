// Command filesystem provides an MCP server for native Android/Termux file access.
// It implements path validation to prevent directory traversal attacks.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kahz12/droidmcp/internal/buildinfo"
	"github.com/kahz12/droidmcp/internal/config"
	"github.com/kahz12/droidmcp/internal/core"
	"github.com/kahz12/droidmcp/internal/logger"
	"github.com/kahz12/droidmcp/internal/pathsec"
	"github.com/mark3labs/mcp-go/mcp"
)

var cfg *config.Config

// defaultMaxReadBytes bounds how many bytes a single read_file call buffers in
// memory, so a huge file cannot OOM the process. Callers can page past it with
// offset/length.
const defaultMaxReadBytes = 10 * 1024 * 1024 // 10 MiB

// maxReadBytes is the active read cap. It is a package var (set once in main
// from DROIDMCP_MAX_READ_BYTES) rather than read per-call, so handlers stay
// pure and easy to test.
var maxReadBytes int64 = defaultMaxReadBytes

// fileEntry is the JSON shape returned by list_directory and stat. Pointer
// fields are nil on platforms (e.g. Windows) where the underlying syscall.Stat_t
// is not available.
type fileEntry struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"` // "file", "dir", "symlink", "other"
	Size     int64   `json:"size"`
	Mode     string  `json:"mode"`       // human form, e.g. "-rw-r--r--"
	ModeOct  string  `json:"mode_octal"` // e.g. "0644"
	Modified string  `json:"modified"`   // RFC3339, UTC
	UID      *uint32 `json:"uid,omitempty"`
	GID      *uint32 `json:"gid,omitempty"`
}

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		logger.Fatal("Failed to load config", err)
	}

	// An explicit DROIDMCP_ROOT is mandatory. The shared config defaults ROOT to
	// "/", which would expose the entire device; the filesystem server is the
	// only one that acts on ROOT, so it fail-fasts here rather than silently
	// granting whole-filesystem access. An empty value is treated as unset.
	if os.Getenv("DROIDMCP_ROOT") == "" {
		logger.Log.Error("mcp-filesystem requires DROIDMCP_ROOT to be set to the directory it may access. Refusing to start (the default of \"/\" would expose the whole device).")
		os.Exit(1)
	}
	// An explicit "/" is just as broad as the default, so refuse it too.
	if err := pathsec.ValidateRoot(cfg.Root); err != nil {
		logger.Log.Error("mcp-filesystem: " + err.Error())
		os.Exit(1)
	}

	// This server grants read/write/delete over ROOT, so it must not run
	// unauthenticated: anything else on localhost (other apps, adb) could
	// otherwise drive it. The API key is mandatory, as in mcp-termux.
	apiKey := config.ResolveAPIKey("filesystem")
	if apiKey == "" {
		logger.Log.Error("mcp-filesystem requires DROIDMCP_FILESYSTEM_KEY or DROIDMCP_API_KEY to be set. Refusing to start.")
		os.Exit(1)
	}

	// Optional override of the read cap (DROIDMCP_MAX_READ_BYTES). Unset,
	// non-numeric or non-positive values leave the default in place.
	if raw := os.Getenv("DROIDMCP_MAX_READ_BYTES"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxReadBytes = int64(n)
		}
	}

	server := core.NewDroidServer("mcp-filesystem", buildinfo.Version)
	server.APIKey = apiKey
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

// registerTools maps MCP tool definitions to their respective Go handlers.
func registerTools(s *core.DroidServer) {
	// read_file: Basic I/O for reading text files. Optional offset/length
	// parameters allow paginating large files without buffering them whole.
	readFileTool := mcp.NewTool("read_file",
		mcp.WithDescription("Read the contents of a file (optionally a byte range via offset/length)"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file relative to root")),
		mcp.WithNumber("offset", mcp.Description("Byte offset to start reading at. Default: 0")),
		mcp.WithNumber("length", mcp.Description("Maximum number of bytes to read. 0 (default) means read to end")),
	)
	s.MCPServer.AddTool(readFileTool, handleReadFile)

	// read_file_lines: Line-oriented reader, friendlier than offset/length for
	// LLM workflows.
	readFileLinesTool := mcp.NewTool("read_file_lines",
		mcp.WithDescription("Read a 1-indexed inclusive range of lines from a file"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file relative to root")),
		mcp.WithNumber("start", mcp.Required(), mcp.Description("First line to read (1-indexed)")),
		mcp.WithNumber("end", mcp.Description("Last line to read (1-indexed, inclusive). 0 (default) means to end of file")),
	)
	s.MCPServer.AddTool(readFileLinesTool, handleReadFileLines)

	// write_file: Creates parent directories if they don't exist.
	writeFileTool := mcp.NewTool("write_file",
		mcp.WithDescription("Write content to a file"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file relative to root")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Content to write")),
	)
	s.MCPServer.AddTool(writeFileTool, handleWriteFile)

	// list_directory: Returns a JSON array of fileEntry objects.
	listDirTool := mcp.NewTool("list_directory",
		mcp.WithDescription("List contents of a directory as a JSON array of file entries"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the directory relative to root")),
	)
	s.MCPServer.AddTool(listDirTool, handleListDirectory)

	// stat: Returns rich metadata for a single path.
	statTool := mcp.NewTool("stat",
		mcp.WithDescription("Return JSON metadata (size, mode, mtime, owner) for a path. Does not follow symlinks"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path relative to root")),
	)
	s.MCPServer.AddTool(statTool, handleStat)

	// search_files: Recursive name search via glob or regex with optional cap.
	searchFilesTool := mcp.NewTool("search_files",
		mcp.WithDescription("Search for files by glob pattern or regex"),
		mcp.WithString("root", mcp.Description("Directory to start search from (relative to root). Default: \".\"")),
		mcp.WithString("pattern", mcp.Description("Glob pattern (filepath.Match syntax). Mutually exclusive with regex")),
		mcp.WithString("regex", mcp.Description("Regular expression matched against the entry name. Mutually exclusive with pattern")),
		mcp.WithNumber("max_results", mcp.Description("Stop walking after this many matches. 0 (default) means unlimited")),
	)
	s.MCPServer.AddTool(searchFilesTool, handleSearchFiles)

	// delete_file: Removes a file or directory; recursive flag opts into rm -rf.
	deleteFileTool := mcp.NewTool("delete_file",
		mcp.WithDescription("Delete a file or directory"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the file/dir relative to root")),
		mcp.WithBoolean("recursive", mcp.Description("If true, remove non-empty directories recursively. Default: false")),
	)
	s.MCPServer.AddTool(deleteFileTool, handleDeleteFile)

	// move_file: Atomically renames/moves files within the same filesystem.
	moveFileTool := mcp.NewTool("move_file",
		mcp.WithDescription("Move or rename a file/directory"),
		mcp.WithString("source", mcp.Required(), mcp.Description("Source path relative to root")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination path relative to root")),
	)
	s.MCPServer.AddTool(moveFileTool, handleMoveFile)

	// copy_file: Recursive copy for files and directories.
	copyFileTool := mcp.NewTool("copy_file",
		mcp.WithDescription("Copy a file, or recursively copy a directory tree (symlinks inside the tree are skipped)"),
		mcp.WithString("source", mcp.Required(), mcp.Description("Source path relative to root")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination path relative to root")),
	)
	s.MCPServer.AddTool(copyFileTool, handleCopyFile)
}

// securePath resolves a relative path against DROIDMCP_ROOT and ensures it stays
// within bounds. It returns an absolute path or an error if a traversal attempt
// is detected; the containment rules live in internal/pathsec.
func securePath(relPath string) (string, error) {
	return pathsec.Secure(cfg.Root, relPath)
}

// buildFileEntry converts an os.FileInfo into the JSON-friendly fileEntry shape.
func buildFileEntry(info fs.FileInfo) fileEntry {
	typ := "file"
	switch {
	case info.IsDir():
		typ = "dir"
	case info.Mode()&os.ModeSymlink != 0:
		typ = "symlink"
	case !info.Mode().IsRegular():
		typ = "other"
	}
	e := fileEntry{
		Name:     info.Name(),
		Type:     typ,
		Size:     info.Size(),
		Mode:     info.Mode().String(),
		ModeOct:  fmt.Sprintf("%#o", info.Mode().Perm()),
		Modified: info.ModTime().UTC().Format(time.RFC3339),
	}
	if uid, gid, ok := ownerOf(info); ok {
		e.UID = &uid
		e.GID = &gid
	}
	return e
}

// Handler implementations follow the standard MCP pattern:
// 1. Extract and validate arguments.
// 2. Resolve secure path.
// 3. Perform OS-level operation.
// 4. Return ToolResult.

func handleReadFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	offset := req.GetInt("offset", 0)
	length := req.GetInt("length", 0)
	if offset < 0 || length < 0 {
		return mcp.NewToolResultError("offset and length must be non-negative"), nil
	}
	// Reject an explicit range larger than the cap up front so the caller
	// knows to page rather than getting a silently truncated result.
	if length > 0 && int64(length) > maxReadBytes {
		return mcp.NewToolResultError(fmt.Sprintf(
			"length %d exceeds max read size of %d bytes; read in smaller ranges", length, maxReadBytes)), nil
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}

	// Explicit range: honor it exactly (already checked <= cap). Fewer bytes
	// near EOF is fine.
	if length > 0 {
		content, err := io.ReadAll(io.LimitReader(f, int64(length)))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(content)), nil
	}

	// Unbounded read: cap memory at maxReadBytes. Read one extra byte so we can
	// tell "exactly at the cap" from "over the cap" and refuse the latter.
	content, err := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if int64(len(content)) > maxReadBytes {
		return mcp.NewToolResultError(fmt.Sprintf(
			"file exceeds max read size of %d bytes; use offset/length to read it in ranges", maxReadBytes)), nil
	}
	return mcp.NewToolResultText(string(content)), nil
}

func handleReadFileLines(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	start, err := req.RequireInt("start")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	end := req.GetInt("end", 0)
	if start < 1 {
		return mcp.NewToolResultError("start must be >= 1"), nil
	}
	if end != 0 && end < start {
		return mcp.NewToolResultError("end must be >= start"), nil
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow long lines without panicking on the default 64KiB token cap.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var out strings.Builder
	line := 0
	for scanner.Scan() {
		line++
		if line < start {
			continue
		}
		if end != 0 && line > end {
			break
		}
		out.WriteString(scanner.Text())
		out.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(out.String()), nil
}

func handleWriteFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully wrote to %s", path)), nil
}

func handleListDirectory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	out := make([]fileEntry, 0, len(entries))
	for _, entry := range entries {
		// entry.Info() may fail under a concurrent rename/delete; in that case
		// we still report the entry's name and type so the listing is not
		// aborted entirely.
		info, infoErr := entry.Info()
		if infoErr != nil || info == nil {
			out = append(out, fileEntry{
				Name: entry.Name(),
				Type: typeFromDirEntry(entry),
			})
			continue
		}
		out = append(out, buildFileEntry(info))
	}
	data, err := json.Marshal(out)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func handleStat(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Lstat so symlinks are reported as such instead of being followed.
	info, err := os.Lstat(fullPath)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := json.Marshal(buildFileEntry(info))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// errStopWalk is a sentinel returned from filepath.WalkDir to short-circuit
// the walk once max_results is reached.
var errStopWalk = errors.New("stop walk: max_results reached")

func handleSearchFiles(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	searchRootRel := req.GetString("root", ".")
	pattern := req.GetString("pattern", "")
	regexStr := req.GetString("regex", "")
	maxResults := req.GetInt("max_results", 0)

	if pattern == "" && regexStr == "" {
		return mcp.NewToolResultError("either pattern or regex must be provided"), nil
	}
	if pattern != "" && regexStr != "" {
		return mcp.NewToolResultError("only one of pattern or regex may be provided"), nil
	}
	if maxResults < 0 {
		return mcp.NewToolResultError("max_results must be >= 0"), nil
	}

	searchRoot, err := securePath(searchRootRel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	var matchFn func(name string) bool
	if pattern != "" {
		// Validate the glob once up front so an invalid pattern (e.g. a stray
		// "[") fails loudly instead of silently returning zero matches.
		if _, err := filepath.Match(pattern, ""); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid pattern %q: %v", pattern, err)), nil
		}
		matchFn = func(name string) bool {
			ok, _ := filepath.Match(pattern, name)
			return ok
		}
	} else {
		re, err := regexp.Compile(regexStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid regex %q: %v", regexStr, err)), nil
		}
		matchFn = re.MatchString
	}

	var matches []string
	err = filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil // Skip items with permission errors
		}
		if matchFn(d.Name()) {
			rel, _ := filepath.Rel(searchRoot, path)
			matches = append(matches, rel)
			if maxResults > 0 && len(matches) >= maxResults {
				return errStopWalk
			}
		}
		return nil
	})

	if err != nil && !errors.Is(err, errStopWalk) {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if len(matches) == 0 {
		return mcp.NewToolResultText("No matches found"), nil
	}
	return mcp.NewToolResultText(strings.Join(matches, "\n")), nil
}

func handleDeleteFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fullPath, err := securePath(path)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	recursive := req.GetBool("recursive", false)

	if recursive {
		if err := os.RemoveAll(fullPath); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Successfully deleted %s (recursive)", path)), nil
	}

	info, statErr := os.Lstat(fullPath)
	if err := os.Remove(fullPath); err != nil {
		// Surface a clearer hint when the user hit a non-empty directory.
		if statErr == nil && info.IsDir() {
			return mcp.NewToolResultError(fmt.Sprintf(
				"%v (pass recursive=true to remove non-empty directories)", err)), nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Successfully deleted %s", path)), nil
}

func handleMoveFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	src, err := req.RequireString("source")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dst, err := req.RequireString("destination")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

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

func handleCopyFile(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	src, err := req.RequireString("source")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dst, err := req.RequireString("destination")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fullSrc, err := securePath(src)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fullDst, err := securePath(dst)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	info, err := os.Lstat(fullSrc)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if info.IsDir() {
		if err := copyDir(fullSrc, fullDst); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	} else {
		if err := copyRegularFile(fullSrc, fullDst); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
	}
	return mcp.NewToolResultText(fmt.Sprintf("Successfully copied %s to %s", src, dst)), nil
}

// copyRegularFile copies a single regular file from src to dst, keeping the
// source's permission bits. Parent directories are created with 0755.
func copyRegularFile(src, dst string) error {
	// Check the type before opening: open(2) on a FIFO blocks until a writer
	// appears, which would hang the handler before the check below could run.
	if info, err := os.Stat(src); err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", src)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	source, err := in.Stat()
	if err != nil {
		return err
	}
	if !source.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", src)
	}
	if target, err := os.Stat(dst); err == nil && os.SameFile(source, target) {
		return fmt.Errorf("source and destination are the same file")
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, source.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copyDir performs a recursive copy of src into dst, preserving directory and
// file modes. Symlinks inside src are skipped on purpose: following them risks
// copying content from outside root. Symlinks already present in dst are
// refused for the same reason (they would redirect a write out of root).
func copyDir(src, dst string) error {
	// Compare real locations, not spellings: an in-root symlink can alias any
	// directory, so a lexical comparison misses a destination that is really
	// inside the source (the copy would recurse into what it just wrote) or an
	// ancestor of it (files would be overwritten before they are read).
	realSrc, err := pathsec.Resolve(src)
	if err != nil {
		return err
	}
	realDst, err := pathsec.Resolve(dst)
	if err != nil {
		return err
	}
	if pathsec.Within(realSrc, realDst) {
		return fmt.Errorf("destination must be outside the source directory")
	}
	if pathsec.Within(realDst, realSrc) {
		return fmt.Errorf("destination must not contain the source directory")
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			// Deliberately skip; see comment above.
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		// The destination root was validated by securePath (an in-root link to a
		// directory is fine). Anything below it is checked here, because a
		// pre-existing destination tree can hold links securePath never saw.
		if rel != "." {
			if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("destination contains a symlink: %s", target)
			}
		}

		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyRegularFile(path, target)
	})
}

// typeFromDirEntry classifies a fs.DirEntry without calling Info().
func typeFromDirEntry(d fs.DirEntry) string {
	switch {
	case d.IsDir():
		return "dir"
	case d.Type()&os.ModeSymlink != 0:
		return "symlink"
	case d.Type().IsRegular():
		return "file"
	default:
		return "other"
	}
}
