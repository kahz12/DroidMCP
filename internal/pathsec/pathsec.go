// Package pathsec confines caller-supplied relative paths to a root directory.
// It is shared by every server that acts on DROIDMCP_ROOT (filesystem, media,
// sqlite) so the containment rules live, and are tested, in one place.
package pathsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxLinkHops bounds how many dangling symlinks CheckNoSymlinkEscape follows by
// hand, so a link loop fails closed instead of spinning.
const maxLinkHops = 40

var errEscapes = errors.New("access denied: path escapes root")

// Secure resolves relPath against root and ensures it stays within bounds. It
// returns an absolute path or an error if a traversal attempt is detected.
func Secure(root, relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", relPath)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(filepath.Join(absRoot, relPath))
	if err != nil {
		return "", err
	}

	// First line of defense: a cheap lexical check that the cleaned target is
	// absRoot or a descendant.
	if !Within(absRoot, absTarget) {
		return "", errEscapes
	}

	// Second line of defense: resolve symlinks along the path and confirm the
	// real location is still inside the real root. A lexical check alone can be
	// defeated by a symlink that lives inside root but points outside it.
	if err := CheckNoSymlinkEscape(absRoot, absTarget); err != nil {
		return "", err
	}
	return absTarget, nil
}

// Within reports whether target is root itself or a descendant of it. Using
// root+separator prevents prefix false positives (/tmp/safe vs /tmp/safevil),
// and trimming a trailing separator keeps a root of "/" from turning into "//".
func Within(root, target string) bool {
	return target == root || strings.HasPrefix(target, strings.TrimSuffix(root, string(filepath.Separator))+string(filepath.Separator))
}

// ValidateRoot rejects a root that resolves to the top of the filesystem:
// honouring it would grant access to the whole device, which is never what a
// sandbox root is meant to do.
func ValidateRoot(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if filepath.Dir(abs) == abs {
		return fmt.Errorf("DROIDMCP_ROOT %q is the filesystem root and would expose the whole device; set it to a specific directory", root)
	}
	return nil
}

// CheckNoSymlinkEscape resolves symlinks in absTarget (and in every parent
// component) and verifies the real path stays within the real root. absTarget
// need not exist yet: the longest existing ancestor is resolved and checked,
// and the not-yet-created remainder cannot itself contain a symlink.
//
// A symlink that dangles (its target does not exist yet) is followed by hand,
// because EvalSymlinks reports it as "does not exist" and a write through it
// would create the target wherever it points. Any resolution error other than
// "does not exist" fails closed.
func CheckNoSymlinkEscape(absRoot, absTarget string) error {
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("cannot resolve root: %w", err)
	}
	return checkResolved(realRoot, absTarget, 0)
}

func checkResolved(realRoot, target string, hops int) error {
	cur := target
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if !Within(realRoot, resolved) {
				return errors.New("access denied: path escapes root via symlink")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("access denied: %w", err)
		}

		if info, lerr := os.Lstat(cur); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			// cur is a dangling symlink. Its parent exists (Lstat succeeded), so
			// resolve the link text relative to the real parent and keep checking.
			if hops >= maxLinkHops {
				return errors.New("access denied: too many levels of symbolic links")
			}
			link, rerr := os.Readlink(cur)
			if rerr != nil {
				return fmt.Errorf("access denied: %w", rerr)
			}
			if !filepath.IsAbs(link) {
				parent, perr := filepath.EvalSymlinks(filepath.Dir(cur))
				if perr != nil {
					return fmt.Errorf("access denied: %w", perr)
				}
				link = filepath.Join(parent, link)
			}
			return checkResolved(realRoot, filepath.Clean(link), hops+1)
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			// Walked past the filesystem root without an existing ancestor.
			// realRoot exists, so this is unreachable in practice; fail closed.
			return errEscapes
		}
		cur = parent
	}
}

// Resolve returns path with every existing symlink resolved. The longest
// existing ancestor is resolved and the not-yet-created remainder is appended
// unchanged, so it works for destinations that do not exist yet. It does not
// follow a dangling link; callers that need that guarantee use Secure first.
func Resolve(path string) (string, error) {
	path = filepath.Clean(path)
	var rest []string
	cur := path
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, rest[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}
