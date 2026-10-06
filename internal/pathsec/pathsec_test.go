package pathsec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithin(t *testing.T) {
	if !Within("/", "/tmp/example") {
		t.Fatal("root of / rejects descendants")
	}
	if Within("/tmp/safe", "/tmp/safe-other/file") {
		t.Fatal("accepted sibling with shared prefix")
	}
	if !Within("/tmp/safe", "/tmp/safe") {
		t.Fatal("root itself must be within root")
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
}

func TestValidateRoot(t *testing.T) {
	if err := ValidateRoot("/"); err == nil {
		t.Fatal("accepted / as root")
	}
	if err := ValidateRoot(t.TempDir()); err != nil {
		t.Fatalf("rejected a normal directory: %v", err)
	}
	link := filepath.Join(t.TempDir(), "rootlink")
	symlinkOrSkip(t, "/", link)
	if err := ValidateRoot(link); err == nil {
		t.Fatal("accepted a symlink to / as root")
	}
}

func TestSecureRejectsTraversalAndAbsolute(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../x", "a/../../x", "/etc/passwd"} {
		if _, err := Secure(root, rel); err == nil {
			t.Errorf("Secure(%q) succeeded, want error", rel)
		}
	}
	got, err := Secure(root, "sub/new.txt")
	if err != nil {
		t.Fatalf("Secure: %v", err)
	}
	if want := filepath.Join(root, "sub", "new.txt"); got != want {
		t.Fatalf("Secure = %q, want %q", got, want)
	}
}

// A symlink inside root whose target does not exist yet must not let a write
// create the target outside root.
func TestSecureRejectsDanglingSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	symlinkOrSkip(t, filepath.Join(outside, "x.sh"), filepath.Join(root, "evil"))
	symlinkOrSkip(t, filepath.Join("..", filepath.Base(outside), "y.sh"), filepath.Join(root, "rel"))
	symlinkOrSkip(t, "evil", filepath.Join(root, "chain"))
	symlinkOrSkip(t, outside, filepath.Join(root, "dirlink"))
	symlinkOrSkip(t, filepath.Join(outside, "missing"), filepath.Join(root, "dangdir"))

	for _, rel := range []string{"evil", "rel", "chain", "dirlink/new.txt", "dangdir/sub/file"} {
		if _, err := Secure(root, rel); err == nil {
			t.Errorf("Secure(%q) succeeded, want escape error", rel)
		}
	}
}

func TestSecureAllowsDanglingSymlinkInsideRoot(t *testing.T) {
	root := t.TempDir()
	symlinkOrSkip(t, "later.txt", filepath.Join(root, "link"))
	if _, err := Secure(root, "link"); err != nil {
		t.Fatalf("in-root dangling link rejected: %v", err)
	}
}

func TestSecureRejectsSymlinkLoop(t *testing.T) {
	root := t.TempDir()
	symlinkOrSkip(t, "b", filepath.Join(root, "a"))
	symlinkOrSkip(t, "a", filepath.Join(root, "b"))
	if _, err := Secure(root, "a"); err == nil {
		t.Fatal("symlink loop accepted")
	}
}

// A loop fails inside EvalSymlinks, so only a long chain of dangling links
// reaches the hop limit that bounds following them by hand.
func TestSecureBoundsDanglingSymlinkChain(t *testing.T) {
	chain := func(links int) string {
		root := t.TempDir()
		for i := 0; i < links; i++ {
			symlinkOrSkip(t, fmt.Sprintf("l%d", i+1), filepath.Join(root, fmt.Sprintf("l%d", i)))
		}
		return root
	}
	if _, err := Secure(chain(maxLinkHops), "l0"); err != nil {
		t.Fatalf("chain of %d dangling links rejected: %v", maxLinkHops, err)
	}
	_, err := Secure(chain(maxLinkHops+1), "l0")
	if err == nil || !strings.Contains(err.Error(), "too many levels") {
		t.Fatalf("chain of %d dangling links = %v, want hop-limit error", maxLinkHops+1, err)
	}
}

func TestResolve(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, real, filepath.Join(base, "alias"))
	got, err := Resolve(filepath.Join(base, "alias", "not", "yet"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(real, "not", "yet"); got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}
