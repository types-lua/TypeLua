package mod

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitTimeout bounds every git invocation.
const gitTimeout = 120 * time.Second

// ErrNoManifest marks a missing tlua.toml at a commit (leaf module).
var ErrNoManifest = fmt.Errorf("no tlua.toml")

// git runs git with a timeout, no terminal prompts, and C locale.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		} else if lines := strings.Split(msg, "\n"); len(lines) > 1 {
			msg = lines[len(lines)-1]
		}
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s: timed out", strings.Join(args, " "))
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// GitAvailable reports whether the git binary works.
func GitAvailable(ctx context.Context) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found in PATH (required for module fetching)")
	}
	if _, err := git(ctx, "", "--version"); err != nil {
		return fmt.Errorf("git not working: %v", err)
	}
	return nil
}

// LsRemote lists refs (tag -> commit, heads) without cloning.
// Peeled `^{}` lines win over tag objects.
func LsRemote(ctx context.Context, url string) (tags, heads map[string]string, err error) {
	out, err := git(ctx, "", "ls-remote", url)
	if err != nil {
		return nil, nil, err
	}
	tags = map[string]string{}
	heads = map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		sha, ref := f[0], f[1]
		if !isHexSHA(sha) {
			continue
		}
		switch {
		case strings.HasPrefix(ref, "refs/tags/"):
			name := strings.TrimPrefix(ref, "refs/tags/")
			if strings.HasSuffix(name, "^{}") {
				tags[strings.TrimSuffix(name, "^{}")] = sha
			} else if _, ok := tags[name]; !ok {
				tags[name] = sha
			}
		case strings.HasPrefix(ref, "refs/heads/"):
			heads[strings.TrimPrefix(ref, "refs/heads/")] = sha
		case ref == "HEAD":
			heads["HEAD"] = sha
		}
	}
	return tags, heads, nil
}

func isHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// CloneMirror clones url as a bare mirror into dir (dir must not exist).
func CloneMirror(ctx context.Context, url, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	_, err := git(ctx, "", "clone", "--mirror", url, dir)
	return err
}

// RemoteUpdate refreshes a mirror.
func RemoteUpdate(ctx context.Context, mirrorDir string) error {
	_, err := git(ctx, "", "-C", mirrorDir, "remote", "update")
	return err
}

// MirrorTags lists tags (name -> commit) from a mirror.
func MirrorTags(ctx context.Context, mirrorDir string) (map[string]string, error) {
	out, err := git(ctx, "", "-C", mirrorDir, "tag", "-l")
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		sha, err := RevParse(ctx, mirrorDir, name)
		if err != nil {
			continue // dangling tag; ignore
		}
		tags[name] = sha
	}
	return tags, nil
}

// RevParse resolves any revision (tag/branch/SHA) to a commit SHA.
func RevParse(ctx context.Context, mirrorDir, rev string) (string, error) {
	out, err := git(ctx, "", "-C", mirrorDir, "rev-parse", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if !isHexSHA(sha) {
		return "", fmt.Errorf("cannot resolve revision %q", rev)
	}
	return sha, nil
}

// ShowFile reads path at commit from a mirror.
// Returns ErrNoManifest-wrapped error when the path is absent.
func ShowFile(ctx context.Context, mirrorDir, commit, path string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", "-C", mirrorDir, "show", commit+":"+path)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if strings.Contains(msg, "does not exist") {
			return nil, fmt.Errorf("%s at %s: %w", path, commit, ErrNoManifest)
		}
		return nil, fmt.Errorf("git show: %s", firstLine(msg))
	}
	return stdout.Bytes(), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	if s == "" {
		return "git failed"
	}
	return s
}

// Extract materializes commit into dst via `git archive` (no tar binary
// needed; extraction uses archive/tar). Regular files only; absolute
// paths, `..` escapes, and non-regular entries are skipped.
func Extract(ctx context.Context, mirrorDir, commit, dst string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", "-C", mirrorDir, "archive", commit)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return err
	}
	xerr := untar(pipe, dst)
	if werr := cmd.Wait(); werr != nil {
		return fmt.Errorf("git archive: %s", firstLine(errBuf.String()))
	}
	return xerr
}

// hasDotDot reports a `..` path element (tar escape attempt).
func hasDotDot(name string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(name), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// untar extracts a tar stream, containing paths inside dst.
func untar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("unpacking module: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		if filepath.IsAbs(hdr.Name) || hasDotDot(hdr.Name) {
			continue // never materialize escapes; anchored fallback below is last resort
		}
		name := filepath.Clean("/" + hdr.Name)
		if name == "/" {
			continue
		}
		target := filepath.Join(dst, name)
		rel, err := filepath.Rel(dst, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue // escape attempt
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		cerr := f.Close()
		if err != nil {
			return err
		}
		if cerr != nil {
			return cerr
		}
	}
}
