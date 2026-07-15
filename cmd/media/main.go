// Command media provides an MCP server for browsing and transforming on-device
// media (images, video, audio). Listing and metadata are served in pure Go;
// conversion, thumbnailing and audio extraction shell out to ffmpeg, with
// optional richer metadata from exiftool. Every path argument is validated
// against DROIDMCP_ROOT to prevent directory traversal, and — like
// mcp-filesystem — the server writes derived files into ROOT and runs
// subprocesses, so it refuses to start without an API key.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	// Register the standard decoders so image.DecodeConfig can report
	// dimensions for the common formats without pulling in any dependency.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// Require an explicit DROIDMCP_ROOT. The shared config defaults ROOT to
	// "/", which would expose (and let ffmpeg write into) the entire device;
	// like mcp-filesystem, this server fail-fasts rather than silently acting
	// on the whole filesystem. An empty value is treated as unset.
	if os.Getenv("DROIDMCP_ROOT") == "" {
		logger.Log.Error("mcp-media requires DROIDMCP_ROOT to be set to the directory it may access. Refusing to start (the default of \"/\" would expose the whole device).")
		os.Exit(1)
	}

	// This server reads ROOT, writes derived files into ROOT and spawns
	// ffmpeg/exiftool, so it must not run unauthenticated: anything else on
	// localhost (other apps, adb) could otherwise drive it. Require an API key,
	// mirroring mcp-filesystem and mcp-termux.
	apiKey := config.ResolveAPIKey("media")
	if apiKey == "" {
		logger.Log.Error("mcp-media requires DROIDMCP_MEDIA_KEY or DROIDMCP_API_KEY to be set. Refusing to start.")
		os.Exit(1)
	}

	server := core.NewDroidServer("mcp-media", buildinfo.Version)
	server.APIKey = apiKey
	registerTools(server)

	if err := server.ServeSSE(cfg.Port); err != nil {
		logger.Fatal("Server failed", err)
	}
}

// registerTools maps MCP tool definitions to their Go handlers.
func registerTools(s *core.DroidServer) {
	// list_media: scan a directory for image/video/audio files.
	s.MCPServer.AddTool(mcp.NewTool("list_media",
		mcp.WithDescription("List media files (images, video, audio) under a directory. Returns a JSON array of {name, path, type, ext, size, modified}."),
		mcp.WithString("path", mcp.Description("Directory to scan, relative to root. Default: \".\"")),
		mcp.WithArray("types", mcp.WithStringItems(),
			mcp.Description("Filter by media kind: any of \"image\", \"video\", \"audio\". Default: all three")),
		mcp.WithBoolean("recursive", mcp.Description("Descend into subdirectories. Default: false")),
		mcp.WithNumber("max_results", mcp.Description("Stop after this many matches. 0 (default) means unlimited")),
	), handleListMedia)

	// get_metadata: pure-Go dimensions for images, plus exiftool tags when it
	// is installed.
	s.MCPServer.AddTool(mcp.NewTool("get_metadata",
		mcp.WithDescription("Read metadata for a media file: size, mtime, kind, and image dimensions. When exiftool is installed, its full tag set is included under \"exif\"."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the media file relative to root")),
	), handleGetMetadata)

	// convert_image: format change and/or resize via ffmpeg.
	s.MCPServer.AddTool(mcp.NewTool("convert_image",
		mcp.WithDescription("Convert an image to another format and/or resize it (via ffmpeg). Output format is taken from the destination extension."),
		mcp.WithString("source", mcp.Required(), mcp.Description("Source image path relative to root")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination path relative to root; extension picks the output format")),
		mcp.WithNumber("width", mcp.Description("Target width in pixels. 0 keeps aspect from height, or original if height is also 0")),
		mcp.WithNumber("height", mcp.Description("Target height in pixels. 0 keeps aspect from width, or original if width is also 0")),
		mcp.WithNumber("quality", mcp.Description("Output quality 1-100 (higher is better). Applied to JPEG destinations only")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 120s, max 600s")),
	), handleConvertImage)

	// thumbnail: single scaled frame from an image or video via ffmpeg.
	s.MCPServer.AddTool(mcp.NewTool("thumbnail",
		mcp.WithDescription("Generate a thumbnail for an image or video (via ffmpeg). For video, a frame is grabbed at `timestamp`."),
		mcp.WithString("source", mcp.Required(), mcp.Description("Source media path relative to root")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination image path relative to root")),
		mcp.WithNumber("width", mcp.Description("Thumbnail width in pixels. Default: 320 (height auto)")),
		mcp.WithNumber("height", mcp.Description("Thumbnail height in pixels. 0 keeps aspect ratio")),
		mcp.WithString("timestamp", mcp.Description("For video: seek position, seconds (\"5\") or HH:MM:SS (\"00:00:05\"). Default: 0")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 120s, max 600s")),
	), handleThumbnail)

	// extract_audio: strip the audio track out of a video via ffmpeg.
	s.MCPServer.AddTool(mcp.NewTool("extract_audio",
		mcp.WithDescription("Extract the audio track from a video (via ffmpeg -vn). Default copies the stream without re-encoding."),
		mcp.WithString("source", mcp.Required(), mcp.Description("Source video path relative to root")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination audio path relative to root")),
		mcp.WithString("codec", mcp.Description("Audio codec, e.g. \"mp3\", \"aac\", \"flac\". Default: \"copy\" (no re-encode)")),
		mcp.WithString("bitrate", mcp.Description("Target bitrate when re-encoding, e.g. \"192k\". Ignored when codec is \"copy\"")),
		mcp.WithNumber("timeout_seconds", mcp.Description("Per-call timeout. Default 120s, max 600s")),
	), handleExtractAudio)
}

// securePath resolves a relative path against DROIDMCP_ROOT and ensures it stays
// within bounds. It returns an absolute path or an error if a traversal attempt
// is detected. It is identical in behaviour to mcp-filesystem's securePath:
// a lexical containment check plus a symlink-escape check that fails closed.
func securePath(relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
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
	if !withinRoot(absRoot, absTarget) {
		return "", errors.New("access denied: path escapes root")
	}
	if err := checkNoSymlinkEscape(absRoot, absTarget); err != nil {
		return "", err
	}
	return absTarget, nil
}

// withinRoot reports whether absTarget is root itself or a descendant of it.
// Using root+separator prevents prefix false positives (/tmp/safe vs
// /tmp/safevil).
func withinRoot(root, absTarget string) bool {
	return absTarget == root || strings.HasPrefix(absTarget, root+string(filepath.Separator))
}

// checkNoSymlinkEscape resolves symlinks in absTarget (and every parent
// component) and verifies the real path stays within the real root. absTarget
// need not exist yet: the longest existing ancestor is resolved and checked.
// Any resolution error other than "does not exist" fails closed.
func checkNoSymlinkEscape(absRoot, absTarget string) error {
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("cannot resolve root: %w", err)
	}
	cur := absTarget
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if !withinRoot(realRoot, resolved) {
				return errors.New("access denied: path escapes root via symlink")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("access denied: %w", err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return errors.New("access denied: path escapes root")
		}
		cur = parent
	}
}

// mediaKind maps a lowercased file extension (with leading dot) to a media
// kind. Extensions absent from the map are treated as non-media.
var mediaKind = map[string]string{
	// images
	".jpg": "image", ".jpeg": "image", ".png": "image", ".gif": "image",
	".bmp": "image", ".webp": "image", ".tiff": "image", ".tif": "image",
	".heic": "image", ".heif": "image", ".svg": "image", ".ico": "image",
	".avif": "image",
	// video
	".mp4": "video", ".mkv": "video", ".webm": "video", ".mov": "video",
	".avi": "video", ".flv": "video", ".wmv": "video", ".m4v": "video",
	".3gp": "video", ".mpeg": "video", ".mpg": "video", ".ts": "video",
	// audio
	".mp3": "audio", ".aac": "audio", ".flac": "audio", ".wav": "audio",
	".ogg": "audio", ".oga": "audio", ".opus": "audio", ".m4a": "audio",
	".wma": "audio", ".amr": "audio", ".mid": "audio", ".midi": "audio",
}

// classifyMedia returns the media kind ("image"/"video"/"audio") for a file
// name, or "" when the extension is not a recognised media type.
func classifyMedia(name string) string {
	return mediaKind[strings.ToLower(filepath.Ext(name))]
}

// mediaEntry is the JSON shape returned by list_media (one per file).
type mediaEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"` // relative to root
	Type     string `json:"type"` // "image", "video" or "audio"
	Ext      string `json:"ext"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"` // RFC3339, UTC
}

func handleListMedia(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel := req.GetString("path", ".")
	base, err := securePath(rel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	recursive := req.GetBool("recursive", false)
	maxResults := req.GetInt("max_results", 0)
	if maxResults < 0 {
		return mcp.NewToolResultError("max_results must be >= 0"), nil
	}

	want, err := parseKindFilter(req.GetStringSlice("types", nil))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	absRoot, err := filepath.Abs(cfg.Root)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	out := make([]mediaEntry, 0, 16)
	appendEntry := func(fullPath, name string, info fs.FileInfo) bool {
		kind := classifyMedia(name)
		if kind == "" {
			return true
		}
		if len(want) > 0 && !want[kind] {
			return true
		}
		relPath, relErr := filepath.Rel(absRoot, fullPath)
		if relErr != nil {
			relPath = name
		}
		out = append(out, mediaEntry{
			Name:     name,
			Path:     relPath,
			Type:     kind,
			Ext:      strings.ToLower(filepath.Ext(name)),
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
		})
		return maxResults == 0 || len(out) < maxResults
	}

	if recursive {
		errStop := errors.New("stop: max_results reached")
		walkErr := filepath.WalkDir(base, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // skip entries we cannot stat (permission, race)
			}
			if d.IsDir() {
				return nil
			}
			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			if !appendEntry(p, d.Name(), info) {
				return errStop
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, errStop) {
			return mcp.NewToolResultError(walkErr.Error()), nil
		}
	} else {
		entries, readErr := os.ReadDir(base)
		if readErr != nil {
			return mcp.NewToolResultError(readErr.Error()), nil
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, infoErr := e.Info()
			if infoErr != nil {
				continue
			}
			if !appendEntry(filepath.Join(base, e.Name()), e.Name(), info) {
				break
			}
		}
	}

	data, err := json.Marshal(out)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// parseKindFilter validates the optional types filter and returns it as a set.
// An empty input means "all kinds" (returned as a nil set the caller treats as
// no filter).
func parseKindFilter(kinds []string) (map[string]bool, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	set := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		k = strings.ToLower(strings.TrimSpace(k))
		switch k {
		case "image", "video", "audio":
			set[k] = true
		case "":
			continue
		default:
			return nil, fmt.Errorf("unknown media type %q (want image, video or audio)", k)
		}
	}
	return set, nil
}

// mediaMeta is the JSON shape returned by get_metadata.
type mediaMeta struct {
	Path     string         `json:"path"`
	Type     string         `json:"type,omitempty"`
	Ext      string         `json:"ext"`
	Size     int64          `json:"size"`
	Modified string         `json:"modified"`
	Width    int            `json:"width,omitempty"`
	Height   int            `json:"height,omitempty"`
	Exif     map[string]any `json:"exif,omitempty"`
	ExifRaw  string         `json:"exif_raw,omitempty"`
}

func handleGetMetadata(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rel, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	full, err := securePath(rel)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if info.IsDir() {
		return mcp.NewToolResultError("path is a directory"), nil
	}

	name := filepath.Base(full)
	meta := mediaMeta{
		Path:     rel,
		Type:     classifyMedia(name),
		Ext:      strings.ToLower(filepath.Ext(name)),
		Size:     info.Size(),
		Modified: info.ModTime().UTC().Format(time.RFC3339),
	}

	// Pure-Go image dimensions for the formats the stdlib can decode headers
	// for (jpeg/png/gif). Failure is non-fatal — exiftool may still fill it in.
	if meta.Type == "image" {
		if w, h, ok := imageDimensions(full); ok {
			meta.Width = w
			meta.Height = h
		}
	}

	// Rich tags when exiftool is available; silently skipped otherwise so the
	// tool stays useful on a minimal install.
	attachExif(ctx, full, &meta)

	data, err := json.Marshal(meta)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// imageDimensions reports width/height for images the stdlib can decode a
// header for, without decoding the full pixel data.
func imageDimensions(path string) (int, int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	c, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, false
	}
	return c.Width, c.Height, true
}

// attachExif runs exiftool -json and merges the first record into meta when the
// binary is present and the call succeeds. Absolute-path fields are dropped so
// the server's on-disk layout is not leaked to the caller.
func attachExif(ctx context.Context, path string, meta *mediaMeta) {
	if err := ensureTool(exiftoolBin(), installExiftoolHint); err != nil {
		return
	}
	res, err := runTool(ctx, exiftoolBin(), []string{"-json", "-n", path}, 30*time.Second)
	if err != nil || res.ExitCode != 0 || len(res.RawStdout) == 0 {
		return
	}
	var records []map[string]any
	if err := json.Unmarshal(res.RawStdout, &records); err != nil || len(records) == 0 {
		meta.ExifRaw = strings.TrimSpace(res.Stdout)
		return
	}
	rec := records[0]
	// These carry the absolute on-disk path; strip them.
	delete(rec, "SourceFile")
	delete(rec, "Directory")
	meta.Exif = rec
}
