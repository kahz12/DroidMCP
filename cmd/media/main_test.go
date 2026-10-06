package main

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kahz12/droidmcp/internal/config"
	"github.com/mark3labs/mcp-go/mcp"
)

// callRequest builds an mcp.CallToolRequest with the given arguments map.
func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{Arguments: args},
	}
}

// resultText concatenates all text-content blocks and reports the IsError flag.
func resultText(t *testing.T, res *mcp.CallToolResult) (string, bool) {
	t.Helper()
	if res == nil {
		t.Fatalf("expected non-nil result")
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// withRoot points the global cfg at a fresh temp directory and returns it.
func withRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcp-media-test")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cfg = &config.Config{Root: dir}
	return dir
}

// mustCall invokes a handler and returns its result, failing on a Go-level error.
func mustCall(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := h(context.Background(), callRequest(args))
	if err != nil {
		t.Fatalf("handler returned Go error: %v", err)
	}
	return res
}

// writePNG creates a w×h PNG at the given root-relative path and returns nothing.
func writePNG(t *testing.T, root, rel string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(full)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func touch(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSecurePath(t *testing.T) {
	root := withRoot(t)
	cases := []struct {
		name    string
		rel     string
		wantErr bool
	}{
		{"valid", "a.jpg", false},
		{"nested", "sub/a.png", false},
		{"escape", "../out.jpg", true},
		{"absolute", "/etc/passwd", true},
		{"dotdot", "sub/../../out.jpg", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := securePath(tc.rel)
			if (err != nil) != tc.wantErr {
				t.Fatalf("securePath(%q) err=%v wantErr=%v", tc.rel, err, tc.wantErr)
			}
			if !tc.wantErr {
				absRoot, _ := filepath.Abs(root)
				if !strings.HasPrefix(got, absRoot) {
					t.Errorf("securePath(%q)=%q, want prefix %q", tc.rel, got, absRoot)
				}
			}
		})
	}
}

func TestClassifyMedia(t *testing.T) {
	cases := map[string]string{
		"photo.JPG":   "image",
		"clip.mp4":    "video",
		"song.flac":   "audio",
		"a.PnG":       "image",
		"notes.txt":   "",
		"archive.zip": "",
		"noext":       "",
	}
	for name, want := range cases {
		if got := classifyMedia(name); got != want {
			t.Errorf("classifyMedia(%q)=%q, want %q", name, got, want)
		}
	}
}

func TestParseKindFilter(t *testing.T) {
	if s, err := parseKindFilter(nil); err != nil || s != nil {
		t.Errorf("empty filter should be nil set, got %v err=%v", s, err)
	}
	s, err := parseKindFilter([]string{"image", "AUDIO"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s["image"] || !s["audio"] || s["video"] {
		t.Errorf("unexpected set: %v", s)
	}
	if _, err := parseKindFilter([]string{"document"}); err == nil {
		t.Errorf("expected error for unknown kind")
	}
}

func TestListMedia(t *testing.T) {
	root := withRoot(t)
	writePNG(t, root, "a.png", 4, 4)
	touch(t, root, "b.mp4")
	touch(t, root, "c.mp3")
	touch(t, root, "notes.txt")
	touch(t, root, "sub/d.jpg")

	decode := func(res *mcp.CallToolResult) []mediaEntry {
		got, isErr := resultText(t, res)
		if isErr {
			t.Fatalf("list_media error: %s", got)
		}
		var entries []mediaEntry
		if err := json.Unmarshal([]byte(got), &entries); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, got)
		}
		return entries
	}

	t.Run("non-recursive excludes subdir and non-media", func(t *testing.T) {
		entries := decode(mustCall(t, handleListMedia, map[string]any{}))
		if len(entries) != 3 {
			t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
		}
		for _, e := range entries {
			if e.Type == "" || strings.Contains(e.Path, "sub") {
				t.Errorf("unexpected entry: %+v", e)
			}
		}
	})

	t.Run("recursive includes subdir", func(t *testing.T) {
		entries := decode(mustCall(t, handleListMedia, map[string]any{"recursive": true}))
		if len(entries) != 4 {
			t.Fatalf("got %d entries, want 4: %+v", len(entries), entries)
		}
	})

	t.Run("types filter", func(t *testing.T) {
		entries := decode(mustCall(t, handleListMedia, map[string]any{
			"recursive": true, "types": []any{"image"},
		}))
		if len(entries) != 2 {
			t.Fatalf("got %d image entries, want 2: %+v", len(entries), entries)
		}
		for _, e := range entries {
			if e.Type != "image" {
				t.Errorf("expected only images, got %+v", e)
			}
		}
	})

	t.Run("max_results caps output", func(t *testing.T) {
		entries := decode(mustCall(t, handleListMedia, map[string]any{
			"recursive": true, "max_results": 2,
		}))
		if len(entries) != 2 {
			t.Fatalf("got %d entries, want 2", len(entries))
		}
	})
}

func TestGetMetadataImageDims(t *testing.T) {
	root := withRoot(t)
	writePNG(t, root, "pic.png", 21, 13)

	got, isErr := resultText(t, mustCall(t, handleGetMetadata, map[string]any{"path": "pic.png"}))
	if isErr {
		t.Fatalf("get_metadata error: %s", got)
	}
	var meta mediaMeta
	if err := json.Unmarshal([]byte(got), &meta); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if meta.Type != "image" {
		t.Errorf("Type=%q, want image", meta.Type)
	}
	if meta.Width != 21 || meta.Height != 13 {
		t.Errorf("dims=%dx%d, want 21x13", meta.Width, meta.Height)
	}
	if meta.Ext != ".png" {
		t.Errorf("Ext=%q, want .png", meta.Ext)
	}
}

func TestGetMetadataRejectsDir(t *testing.T) {
	root := withRoot(t)
	if err := os.Mkdir(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, isErr := resultText(t, mustCall(t, handleGetMetadata, map[string]any{"path": "d"}))
	if !isErr {
		t.Fatalf("expected error for directory path")
	}
}

func TestScaleFilter(t *testing.T) {
	cases := []struct {
		w, h    int
		wantStr string
		wantOK  bool
	}{
		{0, 0, "", false},
		{320, 0, "scale=320:-1", true},
		{0, 240, "scale=-1:240", true},
		{320, 240, "scale=320:240", true},
	}
	for _, c := range cases {
		got, ok := scaleFilter(c.w, c.h)
		if ok != c.wantOK || got != c.wantStr {
			t.Errorf("scaleFilter(%d,%d)=%q,%v want %q,%v", c.w, c.h, got, ok, c.wantStr, c.wantOK)
		}
	}
}

func TestQualityToQV(t *testing.T) {
	if qv := qualityToQV(100); qv != 2 {
		t.Errorf("qualityToQV(100)=%d, want 2", qv)
	}
	if qv := qualityToQV(1); qv != 31 {
		t.Errorf("qualityToQV(1)=%d, want 31", qv)
	}
	// Monotonic: higher quality never yields a worse (larger) qv.
	prev := 99
	for q := 1; q <= 100; q++ {
		qv := qualityToQV(q)
		if qv < 2 || qv > 31 {
			t.Fatalf("qualityToQV(%d)=%d out of [2,31]", q, qv)
		}
		if qv > prev {
			t.Fatalf("qualityToQV not monotonic at q=%d: %d > %d", q, qv, prev)
		}
		prev = qv
	}
	// Out-of-range inputs are clamped.
	if qualityToQV(0) != 31 || qualityToQV(500) != 2 {
		t.Errorf("clamping failed: %d %d", qualityToQV(0), qualityToQV(500))
	}
}

func TestIsJPEG(t *testing.T) {
	for _, p := range []string{"a.jpg", "a.JPG", "a.Jpg", "b.jpeg", "b.JPEG"} {
		if !isJPEG(p) {
			t.Errorf("isJPEG(%q) should be true", p)
		}
	}
	for _, p := range []string{"a.png", "a.webp", "a", "a.jpg.png"} {
		if isJPEG(p) {
			t.Errorf("isJPEG(%q) should be false", p)
		}
	}
}

func TestValidateDims(t *testing.T) {
	if err := validateDims(-1, 0); err == nil {
		t.Errorf("expected error for negative dim")
	}
	if err := validateDims(0, maxDim+1); err == nil {
		t.Errorf("expected error for oversized dim")
	}
	if err := validateDims(320, 240); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidators(t *testing.T) {
	goodTS := []string{"0", "5", "12.5", "00:00:05", "01:23:45", "1:02:03.5"}
	badTS := []string{"", "-5", "abc", "00:5", "5s", "1:2:3;rm"}
	for _, s := range goodTS {
		if !reTimestamp.MatchString(s) {
			t.Errorf("timestamp %q should be valid", s)
		}
	}
	for _, s := range badTS {
		if reTimestamp.MatchString(s) {
			t.Errorf("timestamp %q should be invalid", s)
		}
	}
	for _, s := range []string{"copy", "mp3", "aac", "libmp3lame"} {
		if !reCodec.MatchString(s) {
			t.Errorf("codec %q should be valid", s)
		}
	}
	for _, s := range []string{"", "-acodec", "mp3;rm", "a b"} {
		if reCodec.MatchString(s) {
			t.Errorf("codec %q should be invalid", s)
		}
	}
	for _, s := range []string{"192k", "320000", "1M"} {
		if !reBitrate.MatchString(s) {
			t.Errorf("bitrate %q should be valid", s)
		}
	}
	for _, s := range []string{"-b", "192kk", "abc"} {
		if reBitrate.MatchString(s) {
			t.Errorf("bitrate %q should be invalid", s)
		}
	}
}

func TestPreflight(t *testing.T) {
	root := withRoot(t)
	touch(t, root, "in.png")

	t.Run("ok creates destination parent", func(t *testing.T) {
		src, dst, errRes := preflight("in.png", "out/thumb.jpg")
		if errRes != nil {
			text, _ := resultText(t, errRes)
			t.Fatalf("unexpected error result: %s", text)
		}
		if !strings.HasSuffix(src, "in.png") || !strings.HasSuffix(dst, filepath.Join("out", "thumb.jpg")) {
			t.Errorf("unexpected paths: %s %s", src, dst)
		}
		if _, err := os.Stat(filepath.Join(root, "out")); err != nil {
			t.Errorf("destination parent not created: %v", err)
		}
	})

	t.Run("same source and destination rejected", func(t *testing.T) {
		_, _, errRes := preflight("in.png", "in.png")
		if errRes == nil {
			t.Fatalf("expected error for identical src/dst")
		}
	})

	t.Run("missing source rejected", func(t *testing.T) {
		_, _, errRes := preflight("missing.png", "out.jpg")
		if errRes == nil {
			t.Fatalf("expected error for missing source")
		}
	})

	t.Run("directory source rejected", func(t *testing.T) {
		if err := os.Mkdir(filepath.Join(root, "adir"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, _, errRes := preflight("adir", "out.jpg")
		if errRes == nil {
			t.Fatalf("expected error for directory source")
		}
	})

	t.Run("escaping destination rejected", func(t *testing.T) {
		_, _, errRes := preflight("in.png", "../evil.jpg")
		if errRes == nil {
			t.Fatalf("expected error for escaping destination")
		}
	})

	// A destination that is a second name for the source (hard link, or a
	// symlink under root pointing back at it) must be refused: ffmpeg opens the
	// output with -y, truncating the very file it is still reading.
	for _, kind := range []string{"hardlink", "symlink"} {
		t.Run(kind+" to the source rejected", func(t *testing.T) {
			link := os.Link
			if kind == "symlink" {
				link = os.Symlink
			}
			alias := kind + ".png"
			if err := link(filepath.Join(root, "in.png"), filepath.Join(root, alias)); err != nil {
				t.Skipf("%s unsupported in this environment: %v", kind, err)
			}
			if _, _, errRes := preflight("in.png", alias); errRes == nil {
				t.Fatal("accepted a destination that is the same file as the source")
			}
		})
	}

	// ffmpeg's image2 muxer turns "IMG%d.jpg" into IMG1.jpg, which would
	// overwrite a source named that way though no file has the literal pattern name.
	t.Run("pattern destination rejected", func(t *testing.T) {
		touch(t, root, "IMG1.jpg")
		for _, dst := range []string{"IMG%d.jpg", "IMG%03d.jpg", "out/%d.png"} {
			if _, _, errRes := preflight("IMG1.jpg", dst); errRes == nil {
				t.Errorf("accepted pattern destination %q", dst)
			}
		}
	})

	// A dangling in-root symlink must not let ffmpeg (-y) create its output
	// outside root.
	t.Run("dangling symlink out of root rejected", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "evil.png")
		if err := os.Symlink(outside, filepath.Join(root, "evil.png")); err != nil {
			t.Skipf("symlinks unsupported in this environment: %v", err)
		}
		if _, _, errRes := preflight("in.png", "evil.png"); errRes == nil {
			t.Fatal("accepted a destination that is a dangling symlink out of root")
		}
	})
}

func TestTailString(t *testing.T) {
	if got := tailString("short", 100); got != "short" {
		t.Errorf("tailString short=%q", got)
	}
	got := tailString("abcdefghij", 3)
	if got != "...hij" {
		t.Errorf("tailString cut=%q, want ...hij", got)
	}
}

// TestConvertImageIntegration exercises the real ffmpeg path when the binary is
// available; it is skipped otherwise so the suite still passes on minimal hosts.
func TestConvertImageIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping conversion integration test")
	}
	root := withRoot(t)
	writePNG(t, root, "src.png", 64, 48)

	got, isErr := resultText(t, mustCall(t, handleConvertImage, map[string]any{
		"source": "src.png", "destination": "out/small.jpg", "width": 32,
	}))
	if isErr {
		t.Fatalf("convert_image failed: %s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "out", "small.jpg")); err != nil {
		t.Fatalf("expected output file: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("invalid JSON result: %v\n%s", err, got)
	}
	if payload["ok"] != true {
		t.Errorf("expected ok=true, got %v", payload["ok"])
	}
}

// TestFFmpegMissingHint verifies a clean, actionable error when ffmpeg is not
// resolvable, using the overridable lookPath hook — and that the failed call
// leaves no side effects (the destination's parent directory is not created).
func TestFFmpegMissingHint(t *testing.T) {
	root := withRoot(t)
	touch(t, root, "src.png")

	orig := lookPath
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { lookPath = orig })

	got, isErr := resultText(t, mustCall(t, handleConvertImage, map[string]any{
		"source": "src.png", "destination": "out/converted.jpg",
	}))
	if !isErr {
		t.Fatalf("expected error when ffmpeg missing")
	}
	if !strings.Contains(got, "pkg install ffmpeg") {
		t.Errorf("error should hint at install, got: %s", got)
	}
	// The tool check runs before preflight, so the failed call must not have
	// created the destination's parent directory.
	if _, err := os.Stat(filepath.Join(root, "out")); !os.IsNotExist(err) {
		t.Errorf("destination parent should not exist after a missing-ffmpeg error (stat err = %v)", err)
	}
}

func TestRemovePartialOutput(t *testing.T) {
	dir := t.TempDir()

	t.Run("removes file created by the failed run", func(t *testing.T) {
		p := filepath.Join(dir, "fresh.jpg")
		if err := os.WriteFile(p, []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !removePartialOutput(p, false) {
			t.Fatalf("expected removal of freshly created output")
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("file should be gone, stat err = %v", err)
		}
	})

	t.Run("never removes a pre-existing destination", func(t *testing.T) {
		p := filepath.Join(dir, "existing.jpg")
		if err := os.WriteFile(p, []byte("user data"), 0o644); err != nil {
			t.Fatal(err)
		}
		if removePartialOutput(p, true) {
			t.Fatalf("must not remove a destination that existed before the run")
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("pre-existing file should survive: %v", err)
		}
	})

	t.Run("no-op when nothing was written", func(t *testing.T) {
		if removePartialOutput(filepath.Join(dir, "never-created.jpg"), false) {
			t.Errorf("nothing to remove should report false")
		}
		if removePartialOutput("", false) {
			t.Errorf("empty path should report false")
		}
	})
}

// TestRunToolTimeout verifies that runTool enforces its per-call timeout and
// terminates the child (SIGTERM-first cancel) instead of waiting it out.
func TestRunToolTimeout(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not available")
	}
	start := time.Now()
	res, err := runTool(context.Background(), "sleep", []string{"30"}, 1*time.Second)
	if err != nil {
		t.Fatalf("runTool returned Go error: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("expected TimedOut=true, got %+v", res)
	}
	// 1s timeout + 2s WaitDelay worst case; anything near 30s means the child
	// was never signalled.
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("timeout not enforced: call took %s", elapsed)
	}
}

// TestFailedConvertLeavesNoOutput drives a real ffmpeg failure (garbage input)
// and asserts no partial destination file survives the call.
func TestFailedConvertLeavesNoOutput(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping failure-cleanup integration test")
	}
	root := withRoot(t)
	// A .png that is not a PNG: ffmpeg will fail to decode it.
	if err := os.WriteFile(filepath.Join(root, "garbage.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, isErr := resultText(t, mustCall(t, handleConvertImage, map[string]any{
		"source": "garbage.png", "destination": "out/broken.jpg",
	}))
	if !isErr {
		t.Fatalf("expected ffmpeg failure on garbage input, got: %s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "out", "broken.jpg")); !os.IsNotExist(err) {
		t.Errorf("partial output should not survive a failed run (stat err = %v)", err)
	}
}

// fakeFFmpeg points DROIDMCP_MEDIA_FFMPEG at a stand-in script so the transform
// handlers run without a real ffmpeg (CI has none, and the integration tests
// above skip there). The script records its argv one per line, writes its last
// argument (the output path) as ffmpeg would, and exits with exitCode. It
// returns the file holding the recorded argv.
func fakeFFmpeg(t *testing.T, exitCode int) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "ffmpeg")
	script := "#!" + sh + "\n" +
		"printf '%s\\n' \"$@\" > \"$FAKE_FFMPEG_ARGS\"\n" +
		"for out; do :; done\n" +
		"printf fake > \"$out\"\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envFFmpeg, bin)
	t.Setenv("FAKE_FFMPEG_ARGS", argsFile)
	return argsFile
}

type mediaHandler = func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

func TestTransformHandlersWithFakeFFmpeg(t *testing.T) {
	cases := []struct {
		name     string
		handler  mediaHandler
		src, dst string
		extra    map[string]any
		argv     func(src, dst string) []string
	}{
		{
			name: "convert_image", handler: handleConvertImage,
			src: "in.png", dst: "out/small.jpg",
			extra: map[string]any{"width": 32, "quality": 90},
			argv: func(src, dst string) []string {
				return []string{"-y", "-i", src, "-vf", "scale=32:-1", "-q:v", strconv.Itoa(qualityToQV(90)), dst}
			},
		},
		{
			name: "thumbnail", handler: handleThumbnail,
			src: "clip.mp4", dst: "thumbs/t.jpg",
			extra: map[string]any{"timestamp": "00:00:05"},
			argv: func(src, dst string) []string {
				return []string{"-y", "-ss", "00:00:05", "-i", src, "-frames:v", "1", "-vf", "scale=320:-1", dst}
			},
		},
		{
			name: "extract_audio", handler: handleExtractAudio,
			src: "clip.mp4", dst: "audio/a.mp3",
			extra: map[string]any{"codec": "mp3", "bitrate": "192k"},
			argv: func(src, dst string) []string {
				return []string{"-y", "-i", src, "-vn", "-acodec", "mp3", "-b:a", "192k", dst}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := withRoot(t)
			touch(t, root, tc.src)
			argsFile := fakeFFmpeg(t, 0)
			args := map[string]any{"source": tc.src, "destination": tc.dst}
			for k, v := range tc.extra {
				args[k] = v
			}

			got, isErr := resultText(t, mustCall(t, tc.handler, args))
			if isErr {
				t.Fatalf("%s failed: %s", tc.name, got)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(got), &payload); err != nil || payload["ok"] != true {
				t.Fatalf("unexpected result: %s (%v)", got, err)
			}
			recorded, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatalf("ffmpeg was not run: %v", err)
			}
			want := tc.argv(filepath.Join(root, tc.src), filepath.Join(root, tc.dst))
			if strings.TrimSuffix(string(recorded), "\n") != strings.Join(want, "\n") {
				t.Fatalf("argv:\n%s\nwant:\n%s", recorded, strings.Join(want, "\n"))
			}
		})
	}
}

// Every transform handler must go through preflight: a destination that is the
// source under another name, or a pattern ffmpeg would expand onto it, is
// refused before ffmpeg runs.
func TestTransformHandlersRejectUnsafeDestinations(t *testing.T) {
	handlers := map[string]mediaHandler{
		"convert_image": handleConvertImage,
		"thumbnail":     handleThumbnail,
		"extract_audio": handleExtractAudio,
	}
	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			root := withRoot(t)
			src := filepath.Join(root, "IMG1.jpg")
			if err := os.WriteFile(src, []byte("original"), 0o644); err != nil {
				t.Fatal(err)
			}
			dsts := []string{"IMG1.jpg", "IMG%d.jpg"}
			if err := os.Link(src, filepath.Join(root, "alias.jpg")); err == nil {
				dsts = append(dsts, "alias.jpg")
			}
			argsFile := fakeFFmpeg(t, 0)

			for _, dst := range dsts {
				if _, isErr := resultText(t, mustCall(t, handler, map[string]any{"source": "IMG1.jpg", "destination": dst})); !isErr {
					t.Errorf("accepted destination %q", dst)
				}
			}
			if _, err := os.Stat(argsFile); err == nil {
				t.Fatal("ffmpeg ran for a rejected destination")
			}
			if data, err := os.ReadFile(src); err != nil || string(data) != "original" {
				t.Fatalf("source changed: %q %v", data, err)
			}
		})
	}
}

// A failed run reports an error and removes the output it created, without a
// real ffmpeg.
func TestFailedRunRemovesNewOutput(t *testing.T) {
	root := withRoot(t)
	touch(t, root, "in.png")
	fakeFFmpeg(t, 1)

	got, isErr := resultText(t, mustCall(t, handleConvertImage, map[string]any{"source": "in.png", "destination": "out.jpg"}))
	if !isErr || !strings.Contains(got, `"partial_output_removed":true`) {
		t.Fatalf("expected a failure that removed the partial output, got: %s", got)
	}
	if _, err := os.Stat(filepath.Join(root, "out.jpg")); !os.IsNotExist(err) {
		t.Fatalf("partial output survived: %v", err)
	}
}

func TestToolTimeout(t *testing.T) {
	if got := toolTimeout(callRequest(nil)); got != 0 {
		t.Errorf("absent timeout_seconds = %v, want 0 (runTool's default)", got)
	}
	if got := toolTimeout(callRequest(map[string]any{"timeout_seconds": 30.0})); got != 30*time.Second {
		t.Errorf("timeout_seconds=30 = %v", got)
	}
	if got := toolTimeout(callRequest(map[string]any{"timeout_seconds": 1e6})); got != maxToolTimeout {
		t.Errorf("timeout_seconds=1e6 = %v, want the %v cap", got, maxToolTimeout)
	}
}

// main must refuse an explicit DROIDMCP_ROOT=/. That check lives in main, which
// exits, so the test re-runs its own binary as a child process that calls main.
func TestMainRefusesFilesystemRoot(t *testing.T) {
	if os.Getenv("DROIDMCP_TEST_RUN_MAIN") == "1" {
		main()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainRefusesFilesystemRoot$")
	// No API key in the environment: without the root check, main would exit
	// for that reason instead (rejected below) rather than start serving.
	cmd.Env = []string{"DROIDMCP_TEST_RUN_MAIN=1", "DROIDMCP_ROOT=/"}
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("main with DROIDMCP_ROOT=/: %v, want exit status 1\n%s", err, out)
	}
	if !strings.Contains(string(out), "is the filesystem root") {
		t.Fatalf("main exited for another reason:\n%s", out)
	}
}
