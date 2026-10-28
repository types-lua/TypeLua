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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
const gitTimeout = 120 * time.Second

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
var ErrNoManifest = fmt.Errorf("no tlua.toml")

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：git 调统一穿 timeout(120s)+GIT_TERMINAL_PROMPT=0+LC_ALL=C，错取末行
// （git 错堆栈只留尾），超时另报。障碍是交互卡死：env 钳之；输出乱码：C locale。
// 候选：裸调 vs 包装；选包装。不变量：错文单行。排查现场：卡死先查 env，超时先查
// 超限。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：LookPath+`--version` 双检，缺/坏各报因。障碍是 PATH 无 git：首判。
// 排查现场：fetch 全败先查此。
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
//
// 设计者视角：ls-remote 轻嗅仓，剥离行（annotated tag 真 commit）覆对象行，坏 SHA
// 跳。障碍是轻量 tag vs 注解 tag：剥离优先。候选：clone 全量 vs ls；选 ls。不变量：
// 剥离胜。排查现场：版 commit 错先查剥离。
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
	// 设计者视角：40 位十六进制即 SHA，防短 SHA 误判。障碍是大小写：双容。排查现场：
	// 解析失败先查长度。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：`clone --mirror` 全量裸仓，父目录先建。障碍是已存：调用方 Sync 已判。
// 排查现场：clone 失败先查父目录与网络。
func CloneMirror(ctx context.Context, url, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	_, err := git(ctx, "", "clone", "--mirror", url, dir)
	return err
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：`remote update` 增量拉新 tag/branch，Sync 在线分支调用。障碍是并发
// 更：git 自锁。排查现场：版不新先查此。
func RemoteUpdate(ctx context.Context, mirrorDir string) error {
	_, err := git(ctx, "", "-C", mirrorDir, "remote", "update")
	return err
}

// MirrorTags lists tags (name -> commit) from a mirror.
//
// 设计者视角：`tag -l` 枚举+RevParse 逐解，dangling 跳过（删 tag 残留）。障碍是坏
// tag：跳不崩。候选：packed-refs 直读 vs git 调；选调，兼容。排查现场：版缺先查
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：`rev-parse rev^{commit}` 归一 tag/branch/SHA 为 commit，非 40 位即
// 错。障碍是 tag 对象 vs commit：`^{commit}` 剥之。排查现场：解不出先查后缀。
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

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 返回ErrNoManifest，如 `errnomanifest` 例，排查看 Span。
//
// 设计者视角：`git show commit:path` 读档，不存判 ErrNoManifest 包之（上游判叶），
// 余错取首行。障碍是缺文 vs 坏仓：`does not exist` 分流。候选：archive vs show；
// 选 show 单文件轻。不变量：缺恒包 ErrNoManifest。排查现场：叶误报错先查包。
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
	// 设计者视角：错文取首行（git 末行在 git() 取，此处首行供 show/archive），空兜
	// `git failed`。障碍是多行：截首。排查现场：错文空先查兜底。
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
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// paths, `..` escapes, and non-regular entries are skipped.
//
// 设计者视角：`git archive` 流+untar 落盘，无 tar 二进制依赖；三重防逃
// （绝对/`..`/Rel 越界）保不写出 dst。障碍是恶意 tar 逃逸：见 untar。候选：
// checkout vs archive；选 archive 免工作树。不变量：只落常规文件。排查现场：
// 文件缺失先查过滤，逃逸先查 untar。
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
//
// 设计者视角：按 `/` 切段判 `..`，zip-slip 第一闸。障碍是 `a/../b` 合法但仍拒：
// 宁严。排查现场：文件被跳先查此。
func hasDotDot(name string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(name), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：逐条只落常规，绝对/`..`/Rel 越界全跳，`/hdr` 锚后 Join 保内。
// 障碍是 symlink/设备：跳。候选：全解 vs 过滤；选过滤。不变量：不出 dst。
// 排查现场：解后缺文件先查三闸。
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
