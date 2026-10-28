package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/typelua/typelua/internal/ast"
	"github.com/typelua/typelua/internal/checker"
	"github.com/typelua/typelua/internal/config"
	"github.com/typelua/typelua/internal/diag"
	"github.com/typelua/typelua/internal/emitter"
	"github.com/typelua/typelua/internal/parser"
	"github.com/typelua/typelua/internal/types"
	"github.com/typelua/typelua/internal/watch"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// …（略，详见实现；排查先看 Span/诊断）
// 展开后 AST 复用：reqs 须为解析期快照（见 cachedRequires），否则边漂移。候选：
// 全量重算 vs 缓存；选缓存，稳态零复算。不变量：decl 永不发射。排查现场：边变化
// 未重查先查 reqs 快照。
type fileState struct {
	sf         *SourceFile
	parseDiags []diag.Diagnostic
	checkDiags []diag.Diagnostic
	modTy      types.Type
	reqs       []string
	targets    map[string]string
	emitDirty  bool
	isDecl     bool
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Session 是 watch 的“账房”——files/order 记成员，ambient 记父域，
// unread 记读错，prelude 记铺路决策，lastChecked/diags 供日志。障碍是并发：
// 方法限单 goroutine（watch 循环），内部并行池 Join 后返。候选：锁 vs 单线程；
// 选单线程，零锁。不变量：order 恒有序。排查现场：数据赛跑先查是否多 goroutine
// 调 Sync。
type Session struct {
	cfg            *config.Config
	files          map[string]*fileState
	order          []string
	shorts         map[string]string
	ambient        *checker.Scope
	ambientDiags   []diag.Diagnostic
	ambientContent map[string]string
	unread         map[string][]diag.Diagnostic
	prelude        bool
	initialized    bool
	lastChecked    []string
	diags          []diag.Diagnostic
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：NewSession 只发“空账本”——files/unread 空图，cfg 挂钩。障碍是零值
// Session 误用：files nil 写即 panic，故此处双图初始化。不变量：开箱可 Sync。
// 排查现场：nil map 写 panic 先查是否绕过 NewSession 自造 Session。
func NewSession(cfg *config.Config) *Session {
	return &Session{cfg: cfg, files: map[string]*fileState{}, unread: map[string][]diag.Diagnostic{}}
}

// NewSession如 `f(1)`，排查看 Span。
// NewSession如 `f(1)`，排查看 Span。
//
// 设计者视角：Reset 是配置切换的“清仓”——整 struct 重置，防旧 ambient/shorts
// 残留致错构建。障碍是浅清（只清 files）：ambientContent/prelude 残留即错；故
// 全量重造。候选：diff 配置 vs 全清；选全清，保守正确。不变量：Reset 后同
// NewSession。排查现场：改配置后仍旧行为先查是否忘 Reset。
func (s *Session) Reset(cfg *config.Config) {
	*s = Session{cfg: cfg, files: map[string]*fileState{}, unread: map[string][]diag.Diagnostic{}}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：normKey 是路径的“归一章”——Abs+Clean 双洗，让 watch 绝对路径与
// ListFiles 相对路径同键。障碍是 Abs 失败（坏路径）：退 Clean 不抛。候选：Clean
// vs Abs；选 Abs 优先，跨相对/绝对混跑。不变量：同文件恒同键。排查现场：文件
// 反复重解析先查键是否两制。
func normKey(path string) string {
	if a, err := filepath.Abs(path); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(path)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：targetKey 给 require 边“拍照”——`F:`/`D:` 前缀分运行时/声明，
// nil 记 "nil"，路径经 normKey 保与 files 键同系。障碍是相对 Dir 下 Clean 失配：
// 裸 Clean 让边键与表键错位，误判恒变；故经 normKey。候选：指针比 vs 串比；选串，
// 可存快照比对。不变量：同目标恒同串。排查现场：无改却全量重查先查键系。
func targetKey(tgt *SourceFile) string {
	if tgt == nil {
		return "nil"
	}
	if tgt.IsDecl {
		return "D:" + normKey(tgt.Path)
	}
	return "F:" + normKey(tgt.Path)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 返回Emit，如 `emit` 例，排查看 Span。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Sync 是增量的“总闸”——首轮/满旗走 fullSync 全量建基，否则
// incrSync 差量；config 漂移（watch 误报）降级全量保正确。障碍是半已知成员集：
// ListFiles 失败即返错不动（保上轮好状态）。候选：hint 全信 vs 成员权威；选后者，
// watcher 盲区不致脱钩。不变量：单 goroutine 调。排查现场：改配置未生效先查
// Reset+full，成员不同步先查 ListFiles。
func (s *Session) Sync(full bool, changes []watch.Change, doEmit bool) ([]string, error) {
	if full || !s.initialized {
		return s.fullSync(doEmit)
	}
	for _, c := range changes {
		if c.Config {
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			return s.fullSync(doEmit)
		}
	}
	return s.incrSync(changes, doEmit)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：FileCount 是状态栏的“计数器”——len(files) 直返，与 Program.Files
// 对仗。障碍是 unread 未计入（读错文件不在 files）：状态数与诊断数须分开看。
// 不变量：纯读。排查现场：数对不上先查 unread。
func (s *Session) FileCount() int { return len(s.files) }

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Files 按 order 吐 SourceFile 序，供 watch 清理过期输出。障碍是
// map 乱序：须按 order 重排。不变量：序即 sortedKeys。排查现场：清理漏/误删
// 先查 order 是否 fresh。
func (s *Session) Files() []*SourceFile {
	out := make([]*SourceFile, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.files[k].sf)
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：HasErrors 与 Program 版同闸——扫 s.diags 见 Error 即拦。障碍是
// assembleDiags 时机：Sync 末已组装，直接扫即可。候选：实时算 vs 组装后扫；
// 选后者，单源。不变量：纯读。排查现场：误放行先查 assemble 是否被跳过。
func (s *Session) HasErrors() bool {
	for _, d := range s.diags {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：Diags 给出快照拷贝——append(nil) 防调用方篡改内部。障碍是别名：
// 直返切片会让外部排序污染内部；故拷贝。不变量：返后改之不影响 Session。
// 排查现场：诊断被意外排序先查是否误用 Diags 而非 FormatDiags。
func (s *Session) Diags() []diag.Diagnostic {
	return append([]diag.Diagnostic(nil), s.diags...)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：FormatDiags 是跨 full/incr 的“稳定视图”——同 Program 版按
// 文件-行-列排，屏蔽原始乱序。障碍是增量 unread/ambient/parse/check 四槽拼装
// （见 assembleDiags）本就无序；稳定视图是日志比对之基。不变量：纯读。排查现场：
// 两轮 diff 抖动先查是否直读 Diags。
func (s *Session) FormatDiags() string {
	ds := append([]diag.Diagnostic{}, s.diags...)
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].Span.File != ds[j].Span.File {
			return ds[i].Span.File < ds[j].Span.File
		}
		if ds[i].Span.Start.Line != ds[j].Span.Start.Line {
			return ds[i].Span.Start.Line < ds[j].Span.Start.Line
		}
		return ds[i].Span.Start.Column < ds[j].Span.Start.Column
	})
	var sb strings.Builder
	for _, d := range ds {
		sb.WriteString(d.String())
		sb.WriteString("\n")
	}
	return sb.String()
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：LastChecked 是增量的“验讫章”——返上轮实查重查路径（已排），供测试
// 断言子集、watch 日志。障碍是 full 与 dirty 两路须同语义：full 记全量，incr 记
// 脏闭包。不变量：返拷贝，外部改不动。排查现场：断言失败先查 seedDone 文件是否
// 计入。
func (s *Session) LastChecked() []string {
	return append([]string(nil), s.lastChecked...)
}

func (s *Session) assembleDiags() {
	// 设计者视角：按 unread→parse→ambient→check 拼诊断，unread 键排序保稳。障碍是
	// 增量四槽若漏一槽，FormatDiags 即缺块；故此处全收。候选：实时拼 vs 收官拼；
	// 选收官，Sync 末一次。不变量：s.diags 全量覆盖。排查现场：诊断缺块先查槽是否漏。
	var out []diag.Diagnostic
	keys := make([]string, 0, len(s.unread))
	for k := range s.unread {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, s.unread[k]...)
	}
	for _, k := range s.order {
		out = append(out, s.files[k].parseDiags...)
	}
	out = append(out, s.ambientDiags...)
	for _, k := range s.order {
		out = append(out, s.files[k].checkDiags...)
	}
	s.diags = out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：parseOne 是并行的“纯活”——读盘+Rel+ParseFile，无共享，可任意并发。
// 障碍是 Rel 失败：退原路径保 Span.File 可报。候选：复用 Program.Load 逻辑 vs 独立；
// 选独立纯函数，供 full/incr 共用。不变量：读错即三 nil+err。排查现场：Span 文件名
// 错先查 Rel。
func parseOne(cfg *config.Config, fp string) (content []byte, file *ast.File, pdiags []diag.Diagnostic, readErr error) {
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, nil, nil, err
	}
	rel := fp
	if r, err := filepath.Rel(cfg.Dir, fp); err == nil {
		rel = r
	}
	f, pds := parser.ParseFile(rel, string(data))
	return data, f, pds, nil
}

func readDiag(path string, err error) []diag.Diagnostic {
	// 设计者视角：readDiag 把读错包成 TL4001，与 Load 同形，保 full/incr 诊断一致。
	// 障碍是路径信息：Message 含 err 原文，Span 缺文件（unread 按键归位）。
	// 不变量：恒单条。排查现场：读错码不对先查 Code。
	return []diag.Diagnostic{{Code: diag.CannotReadFile, Message: fmt.Sprintf("cannot read file: %v", err)}}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：fullSync 是“推倒重建”——清图、List+parse、ambient、种指纹、重查、
// 组诊、按需发射，与 fresh Program 逐序一致。障碍是首轮增量基线：须先种 targets
// 指纹，否则次轮全判变。候选：复用 Program vs 自驱；选自驱但同序。不变量：末必
// initialized=true。排查现场：首轮后全量重查先查指纹播种。
func (s *Session) fullSync(doEmit bool) ([]string, error) {
	s.files = map[string]*fileState{}
	s.unread = map[string][]diag.Diagnostic{}
	s.order = nil
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 构建Check，如 `check` 例，排查看 Span。
	if list, err := s.cfg.ListFiles(); err == nil {
		sort.Strings(list)
		s.parseInto(list)
	}
	s.order = s.sortedKeys()
	s.shorts = s.shortModules()
	s.rebuildAmbientFresh()
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		st.targets = s.resolveTargets(st.sf)
	}
	s.recheckAll()
	s.assembleDiags()
	if !doEmit {
		s.initialized = true
		return nil, nil
	}
	if len(s.files) == 0 {
		s.initialized = true
		return nil, fmt.Errorf("no .tl files found")
	}
	emitted, err := s.emitChanged()
	s.initialized = true
	return emitted, err
}

func (s *Session) sortedKeys() []string {
	// 设计者视角：sortedKeys 把 files 键排序作 order，保遍历确定。障碍是 map 随机：
	// 不排即抖。候选：插入序 vs 排序；选排序，与 Program.Load 同。不变量：返有序。
	// 排查现场：输出抖动先查 order 是否经此。
	keys := make([]string, 0, len(s.files))
	for k := range s.files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *Session) shortModules() map[string]string {
	// 设计者视角：短名表借 Program 同源算法，避免两套消歧分叉。障碍是 Session 与
	// Program 的 Cfg 语义须一致；故转调。候选：复制 vs 转调；选转调。不变量：与
	// Program.shortModules 同果。排查现场：短名歧义先查 manifest。
	p := &Program{Cfg: s.cfg}
	return p.shortModules()
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ambient 重建须“洗白再查”——存量 AST 已展开不可复用，故从盘重 parse
// 一遍，保证展开恰一次。障碍是展开两次致宏重跑；障碍二是不可读 decl：跳过保旧域。
// 候选：缓存 AST vs 重 parse；选重 parse，正确压倒省 IO。不变量：decl 永 emitDirty=false。
// 排查现场：宏诊断翻倍先查是否复用旧 AST。
func (s *Session) rebuildAmbientFresh() {
	s.ambientContent = map[string]string{}
	allow := s.cfg.AmbientTypes()
	var decls []*ast.File
	for _, k := range s.order {
		st := s.files[k]
		if !st.isDecl || st.sf.File == nil {
			continue
		}
		if !config.IsAmbientIncluded(st.sf.Path, allow) {
			continue
		}
		data, f, pds, rerr := parseOne(s.cfg, st.sf.Path)
		if rerr != nil {
			continue // unreadable decl: keep old state; unread covers it
		}
		st.sf = &SourceFile{Path: st.sf.Path, Content: string(data), File: f, IsDecl: true}
		st.parseDiags = pds
		st.reqs = checker.RequiredModules(f)
		st.targets = map[string]string{}
		st.emitDirty = false // decls never emit
		s.ambientContent[k] = st.sf.Content
		decls = append(decls, f)
	}
	opt := s.checkerOpt()
	if len(decls) == 0 {
		s.ambient = nil
		s.ambientDiags = nil
		return
	}
	sort.Slice(decls, func(i, j int) bool { return decls[i].Name < decls[j].Name })
	base, diags := checker.BuildAmbient(decls, opt)
	s.ambient = base
	s.ambientDiags = diags
}

func (s *Session) checkerOpt() checker.Options {
	// 设计者视角：checkerOpt 把 Cfg 开关译为检查器口径，与 Program.Check 同源，
	// 保 full/incr 检查一致。障碍是漏抄 Target/NoImplicitAny；故四项齐抄。
	// 不变量：纯读 Cfg。排查现场：增量与全量诊断分叉先查此处。
	return checker.Options{
		Strict:          s.cfg.Strict(),
		StrictNilChecks: s.cfg.StrictNilChecks(),
		NoImplicitAny:   s.cfg.NoImplicitAny(),
		Target:          s.cfg.Target(),
	}
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：fileList 按 order 组 SourceFile 序，供 driver 与发射同序遍历。
// 障碍是 order 与 files 失配（删文件后）：以 order 为准，已删自无。候选：实时排
// vs 存序；选存序。不变量：长即 order 长。排查现场：成员缺失先查 order 刷新。
func (s *Session) fileList() []*SourceFile {
	out := make([]*SourceFile, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.files[k].sf)
	}
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：cachedRequires 给 driver 喂“出厂边表”——展开后 AST 走 live walk
// 会变边，故用解析期快照 reqs。障碍是新文件无快照：退 liveRequires。候选：恒 live
// vs 快照；选快照保 stable。不变量：已跟踪恒返快照。排查现场：重查边漂移先查快照。
func (s *Session) cachedRequires(sf *SourceFile) []string {
	if st, ok := s.files[normKey(sf.Path)]; ok {
		return st.reqs
	}
	return liveRequires(sf)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recheckAll 是 full 的“普查”——decl 预 seed，余者 DFS 全查，onCheck
// 回填 diags/modTy/lastChecked。障碍是 lastChecked 须排序供断言；故末 sort。
// 不变量：decl 不实查。排查现场：decl 被查先查 seed。
func (s *Session) recheckAll() {
	files := s.fileList()
	s.lastChecked = nil
	d := &checkDriver{
		files:    files,
		shorts:   s.shorts,
		cfgDir:   s.cfg.Dir,
		ambient:  s.ambient,
		opt:      s.checkerOpt(),
		requires: s.cachedRequires,
		state:    map[*SourceFile]int{},
		modTy:    map[*SourceFile]types.Type{},
		onCheck: func(sf *SourceFile, ds []diag.Diagnostic, mt types.Type) {
			if st, ok := s.files[normKey(sf.Path)]; ok {
				st.checkDiags = ds
				st.modTy = mt
				s.lastChecked = append(s.lastChecked, normKey(sf.Path))
			}
		},
	}
	for _, sf := range files {
		if sf.IsDecl {
			d.seedDone(sf, types.Any)
		}
	}
	for _, sf := range files {
		d.checkOne(sf)
	}
	sort.Strings(s.lastChecked)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：incrSync 是稳态“差量账”——成员以盘为准、changes 仅 hint；toParse
// 按字节变过滤（touch-noop 保缓存），ambient 变则全脏，targets diff 捕改边，
// expandDirty 扩 SCC+逆闭包，末重查+按需发射。障碍是展开 AST 边污染：全经
// cachedRequires。候选：hint 全信 vs 盘为准；选盘为准。不变量：未变文件比特级
// 不动。排查现场：漏重查先查 targets/expandDirty，误全量先查 ambient 指纹。
func (s *Session) incrSync(changes []watch.Change, doEmit bool) ([]string, error) {
	list, err := s.cfg.ListFiles()
	if err != nil {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		return nil, err
	}
	listed := map[string]bool{}
	for _, fp := range list {
		listed[normKey(fp)] = true
	}
	for k := range s.files {
		if !listed[k] {
			delete(s.files, k)
		}
	}
	for k := range s.unread {
		if !listed[k] {
			delete(s.unread, k)
		}
	}
	changed := map[string]watch.ChangeKind{}
	for _, c := range changes {
		if c.Config {
			continue // unreachable (Sync degrades); ignored defensively
		}
		changed[normKey(c.Path)] = c.Kind
	}
	var toParse []string
	for _, fp := range list {
		k := normKey(fp)
		if _, tracked := s.files[k]; !tracked {
			toParse = append(toParse, fp) // adds + unreadable retries
			continue
		}
		if kind, flagged := changed[k]; flagged && kind != watch.Removed {
			toParse = append(toParse, fp)
		}
	}
	contentChanged := s.parseInto(toParse)
	s.order = s.sortedKeys()
	// 父层详见实现，排查看 Span。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	dirty := map[string]bool{}
	if s.ambientInputsChanged() {
		s.rebuildAmbientFresh()
		for _, k := range s.order {
			if !s.files[k].isDecl {
				dirty[k] = true
			}
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		fresh := s.resolveTargets(st.sf)
		if !equalTargets(fresh, st.targets) {
			dirty[k] = true
		}
		st.targets = fresh
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for _, k := range contentChanged {
		if st, ok := s.files[k]; ok && !st.isDecl {
			dirty[k] = true
		}
	}
	dirty = s.expandDirty(dirty)
	s.recheckDirty(dirty)
	s.assembleDiags()
	if !doEmit {
		s.initialized = true
		return nil, nil
	}
	if len(s.files) == 0 {
		s.initialized = true
		return nil, fmt.Errorf("no .tl files found")
	}
	emitted, err := s.emitChanged()
	s.initialized = true
	return emitted, err
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 返回，排查先看 Span/诊断。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：parseInto 是“字节门卫”——并行读解，同字节即留旧（零展开），新字节
// 建 state 标 emitDirty，读错记 unread 删 state。障碍是发射失败重试：emitDirty 独管，
// 不借 changed 防误重查。不变量：changed 仅新/变字节。排查现场：touch 致重查先查
// 字节比对，读错残留先查 unread 清理。
func (s *Session) parseInto(fps []string) (changed []string) {
	type parsed struct {
		data   []byte
		file   *ast.File
		pdiags []diag.Diagnostic
		rerr   error
	}
	results := make([]parsed, len(fps))
	parallelDo(len(fps), func(i int) {
		data, f, pds, rerr := parseOne(s.cfg, fps[i])
		results[i] = parsed{data: data, file: f, pdiags: pds, rerr: rerr}
	})
	for i, fp := range fps {
		r := results[i]
		key := normKey(fp)
		if r.rerr != nil {
			delete(s.files, key)
			s.unread[key] = readDiag(fp, r.rerr)
			continue
		}
		if st, ok := s.files[key]; ok && string(r.data) == st.sf.Content {
			delete(s.unread, key)
			continue // touch-noop: keep parse, check, emit state
		}
		delete(s.unread, key)
		sf := &SourceFile{Path: fp, Content: string(r.data), File: r.file, IsDecl: strings.HasSuffix(fp, ".d.tl")}
		s.files[key] = &fileState{
			sf:         sf,
			parseDiags: r.pdiags,
			targets:    map[string]string{},
			emitDirty:  true,
			isDecl:     sf.IsDecl,
		}
		s.files[key].reqs = checker.RequiredModules(sf.File)
		changed = append(changed, key)
	}
	return changed
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：ambient 指纹比“成员+字节”——allow 内 decl 数或任一字节异即变。
// 障碍是 exclude 的 decl 不计：须同 rebuild 口径。候选：mtime vs 字节；选字节，
// touch 不惊。不变量：纯判不改。排查现场：decl 改未重查先查 allow 口径。
func (s *Session) ambientInputsChanged() bool {
	allow := s.cfg.AmbientTypes()
	count := 0
	for _, k := range s.order {
		st := s.files[k]
		if !st.isDecl || st.sf.File == nil {
			continue
		}
		if !config.IsAmbientIncluded(st.sf.Path, allow) {
			continue
		}
		count++
		if s.ambientContent[k] != st.sf.Content {
			return true
		}
	}
	return count != len(s.ambientContent)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：resolveTargets 给单文件边“拍照”——经 cachedRequires 取边，经
// targetKey 指纹化。障碍是展开后 walk 变边；故恒用缓存。候选：live vs 缓存；选缓存。
// 不变量：纯算。排查现场：边 diff 抖动先查缓存。
func (s *Session) resolveTargets(sf *SourceFile) map[string]string {
	files := s.fileList()
	out := map[string]string{}
	for _, dotted := range s.cachedRequires(sf) {
		out[dotted] = targetKey(resolveRequireIn(files, s.shorts, s.cfg.Dir, sf, dotted))
	}
	return out
}

func equalTargets(a, b map[string]string) bool {
	// 设计者视角：equalTargets 比两快照边集等不等，长不等速判，逐键比值。障碍是
	// nil vs 空图：len 同为 0 即等。候选：反射 vs 手比；选手比。不变量：纯判。
	// 排查现场：误脏先查键系详见实现，排查看 Span。
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：uniqueStrings 是邻接表的“压缝器”——已排序前提下原地去重，省一次
// 分配。障碍是未排序输入：须调用方先 sort（见 expandDirty）。不变量：前 w 个即
// 结果。排查现场：边重复致 SCC 抖动先查排序。
func uniqueStrings(s []string) int {
	if len(s) < 2 {
		return len(s)
	}
	w := 1
	for r := 1; r < len(s); r++ {
		if s[r] != s[w-1] {
			s[w] = s[r]
			w++
		}
	}
	return w
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：expandDirty 把脏点“涨满”——先 Tarjan 求 SCC（环内一脏全脏，保环内
// any 降级确定），再逆边闭包拉上游。障碍是邻接抖动：须先排+去重，Tarjan 访序才稳；
// decl 边不入图（恒 any 无类型流）。图：脏→SCC 涨→逆闭包。候选：仅逆闭包 vs 含 SCC；
// 选含，否则环内半脏致非确定。不变量：只涨不缩。排查现场：环内诊断抖动先查 SCC，
// 漏上游先查逆边。
func (s *Session) expandDirty(dirty map[string]bool) map[string]bool {
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	var nodes []string
	edges := map[string][]string{}
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		nodes = append(nodes, k)
		for _, tk := range st.targets {
			if len(tk) < 2 || tk[0] != 'F' {
				continue
			}
			// 排查看 Span/诊断，如 `local x=1` 错圈整句。
			if _, ok := s.files[tk[2:]]; ok {
				edges[k] = append(edges[k], tk[2:])
			}
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	for n, tos := range edges {
		sort.Strings(tos)
		edges[n] = tos[:uniqueStrings(tos)]
	}
	for _, scc := range tarjanSCC(nodes, edges) {
		hit := false
		for _, m := range scc {
			if dirty[m] {
				hit = true
				break
			}
		}
		if hit {
			for _, m := range scc {
				dirty[m] = true
			}
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	rev := map[string][]string{}
	for from, tos := range edges {
		for _, to := range tos {
			rev[to] = append(rev[to], from)
		}
	}
	queue := make([]string, 0, len(dirty))
	for m := range dirty {
		queue = append(queue, m)
	}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		for _, dep := range rev[cur] {
			if !dirty[dep] {
				dirty[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return dirty
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：tarjanSCC 是经典 Tarjan 递归版——disc/low+栈求连通块，块内排、
// 块间按最小元排，访序先排 nodes。障碍是递归深：与 checker DFS 同量级，可接受；
// 障碍二是边拷贝：调用方已排重，此处只读。不变量：输入 nodes 全覆盖。排查现场：
// 结果抖动先查边排序，栈失衡先查 low 更新。
func tarjanSCC(nodes []string, edges map[string][]string) [][]string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	disc := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]string
	clock := 0
	sorted := append([]string(nil), nodes...)
	sort.Strings(sorted)
	var visit func(v string)
	visit = func(v string) {
		color[v] = gray
		disc[v] = clock
		low[v] = clock
		clock++
		stack = append(stack, v)
		onStack[v] = true
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		for _, to := range edges[v] {
			if color[to] == white {
				visit(to)
				if low[to] < low[v] {
					low[v] = low[to]
				}
			} else if onStack[to] && disc[to] < low[v] {
				low[v] = disc[to]
			}
		}
		if low[v] == disc[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			sort.Strings(comp)
			out = append(out, comp)
		}
		color[v] = black
	}
	for _, v := range sorted {
		if color[v] == white {
			visit(v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：recheckDirty 是脏子图的“普查”——干净者 seed 旧 modTy，脏者为根 DFS，
// 语义与全量同（SCC 整块+DAG 序）。障碍是 seed 类型陈旧：须为上轮 modTy；障碍二是
// lastChecked 须清零重记。不变量：decl 全 seed。排查现场：干净文件被重查先查 seed，
// 类型回退先查 modTy 缓存。
func (s *Session) recheckDirty(dirty map[string]bool) {
	files := s.fileList()
	byPtr := map[*SourceFile]string{}
	for _, k := range s.order {
		byPtr[s.files[k].sf] = k
	}
	d := &checkDriver{
		files:    files,
		shorts:   s.shorts,
		cfgDir:   s.cfg.Dir,
		ambient:  s.ambient,
		opt:      s.checkerOpt(),
		requires: s.cachedRequires,
		state:    map[*SourceFile]int{},
		modTy:    map[*SourceFile]types.Type{},
		onCheck: func(sf *SourceFile, ds []diag.Diagnostic, mt types.Type) {
			if k, ok := byPtr[sf]; ok {
				if st, ok := s.files[k]; ok {
					st.checkDiags = ds
					st.modTy = mt
				}
				s.lastChecked = append(s.lastChecked, k)
			}
		},
	}
	var roots []*SourceFile
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			d.seedDone(st.sf, types.Any)
			continue
		}
		if dirty[k] {
			roots = append(roots, st.sf)
			continue
		}
		d.seedDone(st.sf, st.modTy)
	}
	s.lastChecked = nil
	for _, sf := range roots {
		d.checkOne(sf)
	}
	sort.Strings(s.lastChecked)
}

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 设计者视角：emitChanged 是“差量发货”——prelude 翻转则全脏，否则仅 emitDirty 者
// 进 compute→write；资产恒刷（dedup 廉价）；末按 jobs 序报全量（非仅本轮写）。
// 障碍是写失败语义：按 emitted 前缀清 dirty，失败重试只补欠账；障碍二是 dirty 清
// 点须在资产前提交（资产失败下轮只补资产）。候选：报增量 vs 报全量；选全量，状态
// 稳。不变量：返序即 jobs 序。排查现场：prelude 翻未全发先查翻转分支，重复发射先查
// dirty 清除。
func (s *Session) emitChanged() ([]string, error) {
	files := s.fileList()
	prog := &Program{Cfg: s.cfg, Files: files}
	prelude := NeedsModPath(prog)
	if prelude != s.prelude {
		for _, k := range s.order {
			if !s.files[k].isDecl {
				s.files[k].emitDirty = true
			}
		}
	}
	opt := emitter.Options{Target: s.cfg.Target(), TreeShake: s.cfg.CompilerOptions.TreeShake}
	jobs := collectEmitJobs(s.cfg, files)
	emittedSet := map[string]bool{}
	for _, jb := range jobs {
		emittedSet[filepath.Clean(jb.dst)] = true
	}
	byDst := map[string]string{}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	dstOf := map[string]string{}
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		if dst, ok := OutputForFile(s.cfg, st.sf.Path); ok {
			byDst[filepath.Clean(dst)] = k
			dstOf[k] = dst
		}
	}
	var todo []emitJob
	for _, jb := range jobs {
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		// 排查看 Span/诊断，如 `local x=1` 错圈整句。
		k, ok := byDst[filepath.Clean(jb.dst)]
		if !ok {
			continue
		}
		if st, ok := s.files[k]; ok && st.emitDirty {
			todo = append(todo, jb)
		}
	}
	computeEmitJobs(todo, opt, prelude)
	emitted, err := writeEmitJobs(todo, nil, emittedSet)
	if err != nil {
		return emitted, err
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	written := map[string]bool{}
	for _, dst := range emitted {
		written[filepath.Clean(dst)] = true
	}
	for _, k := range s.order {
		st := s.files[k]
		if st.isDecl {
			continue
		}
		if dst, ok := dstOf[k]; ok && written[filepath.Clean(dst)] {
			st.emitDirty = false
		}
	}
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	s.prelude = prelude
	assets, err := prog.emitNativeAssets(emittedSet)
	if err != nil {
		return emitted, err
	}
	// 报告Emit，如 `emit` 例，排查看 Span。
	// 排查看 Span/诊断，如 `local x=1` 错圈整句。
	all := make([]string, 0, len(jobs)+len(assets))
	for _, jb := range jobs {
		all = append(all, jb.dst)
	}
	return append(all, assets...), nil
}
