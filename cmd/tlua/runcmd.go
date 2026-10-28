package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	golua "github.com/arnodel/golua/lib"
	"github.com/arnodel/golua/lib/debuglib"
	rt "github.com/arnodel/golua/runtime"
	"github.com/typelua/typelua/internal/compiler"
)

// cmdRun 实现 `tlua run`：类型擦除 + 执行，不留下构建产物。
//
// 默认镜像 `tlua build`（target 5.4，除非 tlua.json/--target 指定），
// 但类型检查默认关闭（--no-check 语义）：只有解析错误阻塞执行。
// 传 --check 获得构建级严格性。
//
// 两种运行时，由标志切换：
// …（略，详见实现；排查先看 Span/诊断）
// 默认 parse 即跑（快），--check 方严。障碍是 .d.tl 入口：拒；障碍是 FFI：
// 内嵌必败，须 --lua 外置。候选：常驻 dist vs 临时；选临时零残留。不变量：
// 错诊先印。排查现场：FFI 失败先查 --lua，入口错先查 OutputForFile。
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	check := fs.Bool("check", false, "type-check before running (default: parse only)")
	luaBin := fs.String("lua", "", "external lua interpreter (e.g. lua5.4, luajit); default: embedded golua")
	target := fs.String("target", "", "lua target: 5.4 or 5.1 (default: tlua.json or 5.4, like build)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("usage: tlua run <entry.tl> [-- args...] [--check] [--lua BIN] [--target 5.4|5.1]")
	}
	entryAbs, err := filepath.Abs(rest[0])
	if err != nil {
		return err
	}
	if strings.HasSuffix(entryAbs, ".d.tl") {
		return fmt.Errorf("cannot run declaration file %s", rest[0])
	}
	scriptArgs := rest[1:]

	tmp, err := os.MkdirTemp("", "tlua-run-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// 加载完整项目（类似无文件参数的 build），使兄弟 require
	// 与入口一同发射并在运行时解析。项目外的独立文件
	// 回退为单文件程序。
	cfg, cfgPath, err := loadCfgFor(nil, tmp, *target, false, false)
	if err != nil {
		return err
	}
	p := compiler.NewProgram(cfg)
	if err := p.Load(); err != nil {
		return err
	}
	if cfgPath == "" || !hasSource(p, entryAbs) {
		cfg, _, err = loadCfgFor([]string{entryAbs}, tmp, *target, false, false)
		if err != nil {
			return err
		}
		p = compiler.NewProgram(cfg)
		if err := p.Load(); err != nil {
			return err
		}
	}
	if len(p.Files) == 0 {
		return fmt.Errorf("no .tl files found")
	}
	if !hasSource(p, entryAbs) {
		return fmt.Errorf("entry %s not found (not covered by tlua.json include)", rest[0])
	}
	if *check {
		p.Check()
		if len(p.Diags) > 0 {
			fmt.Print(p.FormatDiags())
		}
		if p.HasErrors() {
			return fmt.Errorf("found %d error(s)", len(p.Diags))
		}
	} else if p.HasErrors() {
		// 仅解析错误也会阻塞 --no-check 运行：损坏的代码
		// 无法发射，更无法执行。
		fmt.Print(p.FormatDiags())
		return fmt.Errorf("found %d error(s)", len(p.Diags))
	}
	if _, err := p.Emit(); err != nil {
		return err
	}
	entryLua, ok := compiler.OutputForFile(cfg, entryAbs)
	if !ok {
		return fmt.Errorf("cannot map entry %s to an output", rest[0])
	}
	src, err := os.ReadFile(entryLua)
	if err != nil {
		return fmt.Errorf("cannot read emitted entry: %v", err)
	}
	if *luaBin != "" {
		return runExternal(*luaBin, entryLua, scriptArgs)
	}
	return runEmbedded(entryLua, src, tmp, scriptArgs)
}

func hasSource(p *compiler.Program, abs string) bool {
	// 设计者视角：判入口是否源内（去 decl，Abs 比对，败退 Clean 比）。障碍是相对/绝对
	// 混：双比。排查现场：入口不在源先查 Files。
	clean := filepath.Clean(abs)
	for _, sf := range p.Files {
		if sf.IsDecl {
			continue
		}
		if fp, err := filepath.Abs(sf.Path); err == nil {
			if filepath.Clean(fp) == clean {
				return true
			}
		} else if filepath.Clean(sf.Path) == clean {
			return true
		}
	}
	return false
}

// runEmbedded 通过 golua 在进程内执行发射的 Lua。
//
// 设计者视角：golua 进程内跑——装库+Traceback+arg 表（arg[0]=名，类 PUC）+
// package.path 指临时（入口+兄弟+vendored）+双 chunk（setup+入口）。障碍是 C 模块：
// 无加载器，须外置。候选：内 vs 外；选旗切。不变量：arg 表恒置。排查现场：require
// 失败先查 setup path，FFI 失败换 --lua。
func runEmbedded(chunkName string, src []byte, tmp string, scriptArgs []string) error {
	r := rt.New(os.Stdout)
	r.PushContext(rt.RuntimeContextDef{MessageHandler: debuglib.Traceback})
	cleanup := golua.LoadAll(r)
	defer cleanup()
	defer r.Close(nil)

	// arg[0] = 脚本名，arg[1..] = 脚本参数（与 PUC Lua 一致）。
	argTable := rt.NewTable()
	r.SetTable(argTable, rt.IntValue(0), rt.StringValue(chunkName))
	argVals := make([]rt.Value, len(scriptArgs))
	for i, a := range scriptArgs {
		v := rt.StringValue(a)
		r.SetTable(argTable, rt.IntValue(int64(i+1)), v)
		argVals[i] = v
	}
	r.SetTable(r.GlobalEnv(), rt.StringValue("arg"), rt.TableValue(argTable))

	// 通过标准 package.path 机制提供临时目录输出
	//（入口 + 兄弟发射 + vendored lua）。
	setup := fmt.Sprintf("package.path = %s .. \";\" .. (package.path or \"\")",
		luaQuote(tmp+"/?.lua;"+tmp+"/?/init.lua"))
	if err := doChunk(r, "<tlua-run-setup>", []byte(setup), nil); err != nil {
		return err
	}
	return doChunk(r, chunkName, src, argVals)
}

func doChunk(r *rt.Runtime, name string, src []byte, argVals []rt.Value) error {
	// 设计者视角：LoadFromSourceOrCode 载块+Call 运，错包名（载错/运错分报）。
	// 障碍是 traceback：PushContext 已设。排查现场：载错看名，运错看 `!!!`。
	clos, err := r.LoadFromSourceOrCode(name, src, "bt", rt.TableValue(r.GlobalEnv()), true)
	if err != nil {
		return fmt.Errorf("error loading %s: %s", name, err)
	}
	if cerr := rt.Call(r.MainThread(), rt.FunctionValue(clos), argVals, rt.NewTerminationWith(nil, 0, false)); cerr != nil {
		return fmt.Errorf("!!! %s", cerr.Error())
	}
	return nil
}

// runExternal 用外部解释器执行发射的入口。
// 发射的 bootstrap 前导从脚本目录解析兄弟/vendor 模块，
// 因此无需设置 LUA_PATH。
//
// 设计者视角：LookPath 定 bin，直调 `bin entry args...` 三流透传，错报退出码。
// 障碍是前导已铺 path：无需 env。排查现场：bin 缺先查 PATH，码非零看透传。
func runExternal(luaBin, entryLua string, scriptArgs []string) error {
	bin, err := exec.LookPath(luaBin)
	if err != nil {
		return fmt.Errorf("lua interpreter %q not found: %v", luaBin, err)
	}
	cmd := exec.Command(bin, append([]string{entryLua}, scriptArgs...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%s exited with code %d", luaBin, ee.ExitCode())
		}
		return err
	}
	return nil
}

// luaQuote 把 s 渲染为 Lua 双引号字符串字面量。
//
// 设计者视角：`"`/`\`/换行转义，余原样，包双引号供 setup path 嵌。障碍是二进制：
// 按 byte 走。排查现场：path 语法错先查转义。
func luaQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, c := range []byte(s) {
		switch c {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
