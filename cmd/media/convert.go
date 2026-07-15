package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// maxDim caps requested pixel dimensions so a caller cannot ask ffmpeg to
// allocate an absurd frame.
const maxDim = 100000

// Validation patterns for the free-form ffmpeg arguments. Because argv is
// passed to ffmpeg without a shell, the real risk is a value that ffmpeg itself
// would interpret as an extra option; these keep the tokens to safe shapes.
// (Paths never need this treatment: securePath returns an absolute path under
// root, which always begins with "/".)
var (
	reTimestamp = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?|[0-9]{1,2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?)$`)
	reCodec     = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	reBitrate   = regexp.MustCompile(`^[0-9]+[kKmM]?$`)
)

// preflight validates a source/destination pair for the transform tools:
// both resolve safely under root, differ, the source exists as a file, and the
// destination's parent directory is created. On failure it returns a ready
// error result; on success errResult is nil.
func preflight(srcRel, dstRel string) (src, dst string, errResult *mcp.CallToolResult) {
	src, err := securePath(srcRel)
	if err != nil {
		return "", "", mcp.NewToolResultError(err.Error())
	}
	dst, err = securePath(dstRel)
	if err != nil {
		return "", "", mcp.NewToolResultError(err.Error())
	}
	if src == dst {
		return "", "", mcp.NewToolResultError("source and destination must differ")
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", "", mcp.NewToolResultError(err.Error())
	}
	if info.IsDir() {
		return "", "", mcp.NewToolResultError("source is a directory")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", "", mcp.NewToolResultError(err.Error())
	}
	return src, dst, nil
}

func handleConvertImage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	source, err := req.RequireString("source")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	destination, err := req.RequireString("destination")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	width := req.GetInt("width", 0)
	height := req.GetInt("height", 0)
	if err := validateDims(width, height); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Check the external tool before preflight so a missing ffmpeg leaves no
	// side effects (preflight creates the destination's parent directory).
	if err := ensureTool(ffmpegBin(), installFFmpegHint); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	src, dst, errRes := preflight(source, destination)
	if errRes != nil {
		return errRes, nil
	}
	dstExisted := fileExists(dst)

	args := []string{"-y", "-i", src}
	if filter, ok := scaleFilter(width, height); ok {
		args = append(args, "-vf", filter)
	}
	// Quality only maps cleanly onto JPEG's -q:v; ignore it elsewhere so we do
	// not pass a flag the target encoder would reject.
	if q := req.GetInt("quality", 0); q > 0 && isJPEG(dst) {
		args = append(args, "-q:v", strconv.Itoa(qualityToQV(q)))
	}
	args = append(args, dst)

	res, err := runTool(ctx, ffmpegBin(), args, toolTimeout(req))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return renderMediaResult(res, "convert_image", source, destination, dst, dstExisted)
}

func handleThumbnail(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	source, err := req.RequireString("source")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	destination, err := req.RequireString("destination")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	width := req.GetInt("width", 0)
	height := req.GetInt("height", 0)
	if err := validateDims(width, height); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Default to a 320px-wide thumbnail (auto height) when no size is given.
	if width == 0 && height == 0 {
		width = 320
	}

	timestamp := req.GetString("timestamp", "0")
	if !reTimestamp.MatchString(timestamp) {
		return mcp.NewToolResultError("timestamp must be seconds (\"5\") or HH:MM:SS (\"00:00:05\")"), nil
	}

	// ffmpeg check before preflight: no side effects when the tool is missing.
	if err := ensureTool(ffmpegBin(), installFFmpegHint); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	src, dst, errRes := preflight(source, destination)
	if errRes != nil {
		return errRes, nil
	}
	dstExisted := fileExists(dst)

	// -ss before -i is input seeking (fast); harmless at 0 for still images.
	args := []string{"-y", "-ss", timestamp, "-i", src, "-frames:v", "1"}
	if filter, ok := scaleFilter(width, height); ok {
		args = append(args, "-vf", filter)
	}
	args = append(args, dst)

	res, err := runTool(ctx, ffmpegBin(), args, toolTimeout(req))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return renderMediaResult(res, "thumbnail", source, destination, dst, dstExisted)
}

func handleExtractAudio(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	source, err := req.RequireString("source")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	destination, err := req.RequireString("destination")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	codec := req.GetString("codec", "copy")
	if !reCodec.MatchString(codec) {
		return mcp.NewToolResultError("codec must be alphanumeric (e.g. \"copy\", \"mp3\", \"aac\")"), nil
	}
	bitrate := req.GetString("bitrate", "")
	if bitrate != "" && !reBitrate.MatchString(bitrate) {
		return mcp.NewToolResultError("bitrate must look like \"192k\" or \"320000\""), nil
	}

	// ffmpeg check before preflight: no side effects when the tool is missing.
	if err := ensureTool(ffmpegBin(), installFFmpegHint); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	src, dst, errRes := preflight(source, destination)
	if errRes != nil {
		return errRes, nil
	}
	dstExisted := fileExists(dst)

	args := []string{"-y", "-i", src, "-vn"}
	if codec == "copy" {
		args = append(args, "-acodec", "copy")
	} else {
		args = append(args, "-acodec", codec)
		if bitrate != "" {
			args = append(args, "-b:a", bitrate)
		}
	}
	args = append(args, dst)

	res, err := runTool(ctx, ffmpegBin(), args, toolTimeout(req))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return renderMediaResult(res, "extract_audio", source, destination, dst, dstExisted)
}

// validateDims rejects negative or oversized pixel dimensions.
func validateDims(width, height int) error {
	if width < 0 || height < 0 {
		return fmt.Errorf("width and height must be >= 0")
	}
	if width > maxDim || height > maxDim {
		return fmt.Errorf("width and height must be <= %d", maxDim)
	}
	return nil
}

// scaleFilter builds an ffmpeg "scale=w:h" filter. A zero dimension becomes -1
// (keep aspect ratio). When both are zero there is nothing to scale and ok is
// false.
func scaleFilter(width, height int) (string, bool) {
	if width <= 0 && height <= 0 {
		return "", false
	}
	w, h := -1, -1
	if width > 0 {
		w = width
	}
	if height > 0 {
		h = height
	}
	return fmt.Sprintf("scale=%d:%d", w, h), true
}

// isJPEG reports whether path has a JPEG extension (case-insensitive, matching
// classifyMedia's lowercased-extension convention).
func isJPEG(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return true
	default:
		return false
	}
}

// qualityToQV maps a user-facing quality (1-100, higher is better) onto
// ffmpeg's mjpeg -q:v scale (2 best .. 31 worst).
func qualityToQV(quality int) int {
	if quality < 1 {
		quality = 1
	}
	if quality > 100 {
		quality = 100
	}
	qv := 31 - int(math.Round(float64(quality-1)/99.0*29.0))
	if qv < 2 {
		qv = 2
	}
	if qv > 31 {
		qv = 31
	}
	return qv
}

// toolTimeout reads the optional timeout_seconds argument and clamps it to the
// allowed range. 0 lets runTool apply its default.
func toolTimeout(req mcp.CallToolRequest) time.Duration {
	t := req.GetInt("timeout_seconds", 0)
	if t <= 0 {
		return 0
	}
	d := time.Duration(t) * time.Second
	if d > maxToolTimeout {
		return maxToolTimeout
	}
	return d
}

// renderMediaResult turns a completed tool run into the JSON tool response.
// Non-zero exit / timeout / cancel are surfaced as error results with a tail of
// stderr (ffmpeg puts the useful diagnostics at the end). On failure, a partial
// output file created by the failed run is removed — but never a file that
// already existed before the call (see removePartialOutput).
func renderMediaResult(res *toolResult, tool, srcRel, dstRel, absDst string, dstExisted bool) (*mcp.CallToolResult, error) {
	ok := res.ExitCode == 0 && !res.TimedOut && !res.Cancelled
	out := map[string]any{
		"ok":          ok,
		"tool":        tool,
		"source":      srcRel,
		"destination": dstRel,
		"exit_code":   res.ExitCode,
		"duration_ms": res.DurationMs,
	}
	if res.TimedOut {
		out["timed_out"] = true
	}
	if res.Cancelled {
		out["cancelled"] = true
	}
	if res.Truncated {
		out["truncated"] = true
	}
	if !ok {
		if removePartialOutput(absDst, dstExisted) {
			out["partial_output_removed"] = true
		}
		out["stderr"] = tailString(res.Stderr, 2000)
		body, _ := json.Marshal(out)
		return mcp.NewToolResultError(string(body)), nil
	}
	body, _ := json.Marshal(out)
	return mcp.NewToolResultText(string(body)), nil
}

// fileExists reports whether path exists (regardless of type).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// removePartialOutput deletes the output a failed ffmpeg run may have left
// behind, and reports whether a file was actually removed. It only acts when
// the destination did NOT exist before the run: a pre-existing file may be the
// user's data (already clobbered by ffmpeg -y, which we cannot undo), and
// deleting it too would compound the damage.
func removePartialOutput(absDst string, dstExisted bool) bool {
	if dstExisted || absDst == "" {
		return false
	}
	if !fileExists(absDst) {
		return false
	}
	return os.Remove(absDst) == nil
}

// tailString returns the last n characters of s (rune-safe), so an error
// message keeps the most relevant final lines without ballooning the response.
func tailString(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "..." + string(r[len(r)-n:])
}
