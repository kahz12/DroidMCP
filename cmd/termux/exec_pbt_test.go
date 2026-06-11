package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
	"time"
	"unicode/utf8"
)

func TestPBT_CappedBufferInvariants(t *testing.T) {
	f := func(chunks [][]byte, max uint16) bool {
		cb := &cappedBuffer{max: int64(max)}
		total := 0
		for _, c := range chunks {
			n, err := cb.Write(c)
			if n != len(c) || err != nil {
				return false
			}
			total += len(c)
		}
		expLen := min(int64(total), int64(max))
		if int64(len(cb.Bytes())) != expLen {
			return false
		}
		return cb.truncated == (int64(total) != expLen)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_CappedBufferPrefix(t *testing.T) {
	f := func(chunks [][]byte, max uint16) bool {
		cb := &cappedBuffer{max: int64(max)}
		var full []byte
		for _, c := range chunks {
			cb.Write(c)
			full = append(full, c...)
		}
		limit := min(int(max), len(full))
		return reflect.DeepEqual(cb.Bytes(), append([]byte{}, full[:limit]...))
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_CappedBufferDefensiveCopy(t *testing.T) {
	f := func(data []byte, max uint16) bool {
		cb := &cappedBuffer{max: int64(max)}
		cb.Write(data)
		first := cb.Bytes()
		if len(first) == 0 {
			return true
		}
		for i := range first {
			first[i] ^= 0xFF
		}
		return !reflect.DeepEqual(first, cb.Bytes())
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 2000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_SafeUTF8AlwaysValid(t *testing.T) {
	f := func(b []byte) bool {
		return utf8.ValidString(safeUTF8(b))
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 10000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_SafeUTF8Idempotent(t *testing.T) {
	f := func(b []byte) bool {
		once := safeUTF8(b)
		return once == safeUTF8([]byte(once))
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 10000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_SafeUTF8IdentityOnValid(t *testing.T) {
	f := func(s string) bool {
		return safeUTF8([]byte(s)) == s
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_AllowlistEmptyAllowsAll(t *testing.T) {
	t.Setenv(allowlistEnv, "")
	f := func(cmd string) bool {
		return allowlistCheck(cmd) == nil
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_ParseAllowlistOrderIndependent(t *testing.T) {
	f := func(tokens []string) bool {
		rev := make([]string, len(tokens))
		for i := range tokens {
			rev[len(tokens)-1-i] = tokens[i]
		}
		a := parseAllowlist(strings.Join(tokens, ","))
		b := parseAllowlist(strings.Join(rev, ","))
		return reflect.DeepEqual(a, b)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_AllowlistBasenameEquivalence(t *testing.T) {
	t.Setenv(allowlistEnv, "ls")
	f := func(dir string) bool {
		return allowlistCheck(filepath.Join(dir, "ls")) == nil
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 3000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_AllowlistDenyInvariant(t *testing.T) {
	const only = "droidmcp_only_allowed"
	t.Setenv(allowlistEnv, only)
	f := func(cmd string) bool {
		if strings.TrimSpace(cmd) == "" {
			return true
		}
		if cmd == only || filepath.Base(cmd) == only {
			return true
		}
		return allowlistCheck(cmd) != nil
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_TimeoutFromReqClamped(t *testing.T) {
	f := func(secs int) bool {
		d := timeoutFromReq(callRequest(map[string]any{"timeout_seconds": secs}))
		if secs == min(secs, 0) {
			return d == 0
		}
		return d != 0 && d == max(d, 0) && d == min(d, maxExecTimeout)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}

func TestPBT_TimeoutFromReqMonotonic(t *testing.T) {
	call := func(s int) time.Duration {
		return timeoutFromReq(callRequest(map[string]any{"timeout_seconds": s}))
	}
	f := func(x, y int) bool {
		lo, hi := min(x, y), max(x, y)
		dl, dh := call(lo), call(hi)
		return dl == min(dl, dh)
	}
	if err := quick.Check(f, &quick.Config{MaxCount: 5000}); err != nil {
		t.Fatal(err)
	}
}
