package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// CompilerOptions 镜像 tsconfig.compilerOptions（子集）。
//
// 设计者视角：它是 tlua 与 tsconfig 世界的“翻译插头”——字段名故意对齐 target/strict/
// outDir/rootDir/sourceMap/types/typeRoots，让前端用户零学习成本。障碍是 Go 零值含糊
// （false 与“未填”分不清）：Strict/StrictNilChecks/NoImplicitAny 用 *bool 区分“未填用默认”
// 与“显式关”，Target/OutDir/RootDir 用空串表未填。取舍是只镜像子集，不全量跟随 tsconfig，
// 未知字段由 json 忽略，换 schema 稳定。不变量：零值 CompilerOptions 经访问方法解释后恒合法。
// 排查现场：若 strict 关不掉，先看是否只置了 Strict 而 StrictNilChecks 仍继承 Strict，
// 次看指针是否取了临时变量地址后被复用。
type CompilerOptions struct {
	Target          string   `json:"target"` // "5.4" | "5.1"
	Strict          *bool    `json:"strict"`
	StrictNilChecks *bool    `json:"strictNilChecks"`
	NoImplicitAny   *bool    `json:"noImplicitAny"`
	OutDir          string   `json:"outDir"`
	RootDir         string   `json:"rootDir"`
	SourceMap       bool     `json:"sourceMap"`
	TreeShake       bool     `json:"treeShake,omitempty"`
	Types           []string `json:"types,omitempty"`     // ambient .d.tl 允许列表（nil/空 = 全部）
	TypeRoots       []string `json:"typeRoots,omitempty"` // 额外 .d.tl 根（默认：types + tlua_modules/@types）
}

// Config 是 tlua.json 的内存表示。
//
// 物理布局：
//
//	Config
//	├── CompilerOptions  // 目标/严格/输出/根目录/SourceMap/TreeShake/Types/TypeRoots
//	├── Include []string // 包含 glob
//	├── Exclude []string // 排除 glob
//	├── Files   []string // 显式文件列表（优先）
//	├── Dir     string   // 配置文件所在目录（不序列化）
//	└── Path    string   // 配置文件绝对路径（不序列化）
//
// 设计者视角：Config 是纯数据“图纸”，不是施工队——它只描述想要什么，从不校验目录存否、
// 从不装配 Program，校验留给 compiler/Open（只有打开时才知 comparator 对不对得上）。
// 障碍是同一图纸在不同打开场景语义不同：把校验塞进来会让单测造个 Config 都得拖半个编译器。
// 取舍是 Dir/Path 打 json:"-" 不序列化，让落盘与内存解耦。不变量：零值经访问方法仍得默认；
// Files 非空时优先于 Include/Exclude。排查现场：若配置改了不生效，先看读的是 Dir 相对路径
// 还是 CWD 相对路径，次看 Files 是否劫持了 include。
type Config struct {
	// CompilerOptions 是 tlua.json 的 compilerOptions 子集。
	CompilerOptions CompilerOptions `json:"compilerOptions"`
	// Include 是包含模式列表（glob）。
	Include []string `json:"include"`
	// Exclude 是排除模式列表（glob）。
	Exclude []string `json:"exclude"`
	// Files 是显式文件列表（优先于 include/exclude）。
	Files []string `json:"files"`
	// Dir 是配置文件所在目录（不序列化）。
	Dir string `json:"-"`
	// Path 是配置文件绝对路径（不序列化）。
	Path string `json:"-"`
}

// Default 返回一个带默认值的 Config（target 5.4、strict、outDir dist、rootDir .）。
//
// 设计者视角：它是“开箱即用的毛坯房”——Load 先以 Default 打底再 Unmarshal 覆盖，
// 让缺字段的 tlua.json 仍得完整语义。障碍是 Strict 为 *bool：此处取局部变量地址装 true，
// 让“未填即严”落地；`_ = t` 的旧占位已无用但留着无害（只动注释故不动它）。
// 取舍是 Dir 原样收下（相对即相对，Abs 化留给 FindUp/Load），Path 先空着等 Load 回填。
// 不变量：返回永不为 nil；CompilerOptions.Target/OutDir/RootDir 开箱即合法。
// 排查现场：若默认 target 忽变，先看本函数而非各访问方法；若 Strict 指针别名串改，
// 先查是否有人把返回的 *bool 拿去改了底层。
func Default(dir string) *Config {
	t := true
	_ = t
	strict := true
	return &Config{
		CompilerOptions: CompilerOptions{Target: "5.4", Strict: &strict, OutDir: "dist", RootDir: "."},
		Dir:             dir,
	}
}

// 带默认值的有效值。
//
// 设计者视角：下七个访问方法是“0 值翻译官”——Config 存的是用户原文（含空），
// 方法吐的是有效值（含默认），让调用方永不判空。取舍是每次现算而不回填原字段，
// 让“用户填了什么”与“实际用什么”可同时追溯。不变量：同一 Config 多次调用结果一致。
// Target 返回有效目标 Lua 版本（默认 5.4）。
//
// 设计者视角：它是发射后端的“方言开关”——"5.4" 与 "5.1" 的整数除法、位操作语义不同，
// 空串即回 5.4 保新行为。排查现场：若 5.1 语义未生效，先看 tlua.json 的大小写与空格
// （此处严格比对，不剪不折），次看调用方是否缓存了旧值。
func (c *Config) Target() string {
	if c.CompilerOptions.Target == "" {
		return "5.4"
	}
	return c.CompilerOptions.Target
}

// Strict 返回是否启用严格模式（默认 true）。
//
// 设计者视角：它是总闸——nil 即 true，让“不填即严”落地；显式 false 才放行。
// 障碍是指针语义：直接解引用会 panic，故先判 nil。排查现场：若严模式关不掉，
// 先打印原始指针是否为 nil（nil 恒严），次看 StrictNilChecks 是否仍继承本值。
func (c *Config) Strict() bool {
	if c.CompilerOptions.Strict != nil {
		return *c.CompilerOptions.Strict
	}
	return true
}

// StrictNilChecks 返回是否启用严格 nil 检查（默认继承 Strict）。
//
// 设计者视角：它是分闸——显式值优先，nil 则继承 Strict()，让 `strict:false` 一键全松、
// `strict:true + strictNilChecks:false` 精细松。取舍是继承链只有一层，不做多级回退，
// 心智负担最小。不变量：StrictNilChecks()==false 则调用方可假定 nil 可赋任意。
// 排查现场：若 nil 检查忽严忽松，先看两层指针各为 nil 还是显值，再看是否有人绕过本方法
// 直读 CompilerOptions 字段。
func (c *Config) StrictNilChecks() bool {
	if c.CompilerOptions.StrictNilChecks != nil {
		return *c.CompilerOptions.StrictNilChecks
	}
	return c.Strict()
}

// NoImplicitAny 返回是否禁止隐式 any（默认 false）。
//
// 设计者视角：它与 Strict 家族反向默认——缺省 false（放行），显式 true 才拦，
// 让渐进迁移的老项目不被一夜全红。障碍是新手常以为 strict:true 即含 noImplicitAny：
// 此处刻意不继承，显式才算数。不变量：默认宽松，收紧必须用户签字。
// 排查现场：若隐式 any 拦不住，先看是否误以为 Strict 会连带它，次看是否调错了方法。
func (c *Config) NoImplicitAny() bool {
	if c.CompilerOptions.NoImplicitAny != nil {
		return *c.CompilerOptions.NoImplicitAny
	}
	return false
}

// OutDir 返回有效输出目录（默认 dist）。
//
// 设计者视角：它是发射的“卸货场”——空即 dist，让 ListFiles/Watch 跳过它防自吞。
// 障碍是相对路径：此处原样返回，绝对化留给调用方 Join(Dir)，避免 Dir 未就绪时早算错。
// 不变量：永不返回空串。排查现场：若构建把 dist 又当源吞回，先看跳过逻辑是否用的本方法
// 而非直读字段（字段可为空，方法恒有值）。
func (c *Config) OutDir() string {
	if c.CompilerOptions.OutDir == "" {
		return "dist"
	}
	return c.CompilerOptions.OutDir
}

// RootDir 返回有效源根目录（默认 .）。
//
// 设计者视角：它是源码的“圈地桩”——ListFiles 的 Walk 以 Join(Dir, RootDir()) 为根，
// 空即 "."（配置目录本身）。取舍是不校验存在，缺失即 Walk 得空集，报错留给上层。
// 不变量：永不返回空串。排查现场：若文件全丢，先看 RootDir 是否指错（如 "src" 但仓库是 "."），
// 次看 Dir 是否为相对路径导致 Join 后漂移。
func (c *Config) RootDir() string {
	if c.CompilerOptions.RootDir == "" {
		return "."
	}
	return c.CompilerOptions.RootDir
}

// TypeRoots 列出额外的 ambient 类型根（类似 tsconfig typeRoots）。
// 相对条目相对于配置目录解析；缺失目录被 ListFiles 忽略。
// 默认覆盖两个常规位置。
//
// 设计者视角：它是声明文件的“种草地”——types/ 与 tlua_modules/@types/ 两块默认草地，
// 用户还可加草地。障碍是相对路径：此处统一 Join(Dir) 转绝对，让 Walk 与去重口径一致；
// 缺失目录不报错直接忽略，让新项目零 types 也能跑。取舍是返回新切片（不暴露底层），
// 调用方随便改不污染配置。不变量：永不返回 nil 语义的空（无配置即两默认）；条目恒绝对或已 Join。
// 排查现场：若 .d.tl 明明存在却不生效，先打印本方法返回值看根是否指偏，次看 Exclude 是否误杀。
func (c *Config) TypeRoots() []string {
	if len(c.CompilerOptions.TypeRoots) > 0 {
		out := make([]string, 0, len(c.CompilerOptions.TypeRoots))
		for _, r := range c.CompilerOptions.TypeRoots {
			if !filepath.IsAbs(r) {
				r = filepath.Join(c.Dir, r)
			}
			out = append(out, r)
		}
		return out
	}
	return []string{
		filepath.Join(c.Dir, "types"),
		filepath.Join(c.Dir, "tlua_modules", "@types"),
	}
}

// AmbientTypes 返回 `types` 允许列表（nil/空 = 包含所有
// ambient .d.tl 文件，镜像 tsconfig 行为）。
//
// 设计者视角：它是 ambient 的“白名单”——空即全收（新项目零配置可用），非空即按名/目录/路径
// 三档过滤（见 IsAmbientIncluded）。取舍是原样返回底层切片（只读约定），换零拷贝。
// 不变量：nil 与空切片语义相同（全含）。排查现场：若加了 types 却全丢，先看是写了包名还是文件名
// （本处要的是 "foo" 而非 "foo.d.tl"，但 IsAmbientIncluded 对两者都 tolerant，重点查目录基名）。
func (c *Config) AmbientTypes() []string {
	return c.CompilerOptions.Types
}

// IsAmbientIncluded 报告 .d.tl 路径在给定允许列表下是否作为 ambient 参与。
// 空允许列表包含全部。
//
// 设计者视角：它是白名单的“验票员”——好比检票口认票（名）也认座区（目录）：
// 先比文件名去后缀（foo.d.tl→foo）与父目录基名，再比 /@types/<t>/ 与 /types/<t>/ 路段，
// 末了比后缀兜底。障碍是布局多样（扁平 types/foo.d.tl vs 嵌套 @types/foo/index.d.tl）：
// 单比一名会漏，故四档渐宽。取舍是纯字符串判定，不触磁盘，大小写敏感。
// 不变量：allow 为空恒 true；t 的首尾空格被忽略，空条目跳过。
// 排查现场：若某包总被漏，先打印 ToSlash 后的 path 看是 "/" 还是 "\\"（本处已归一），
// 次看 allow 写的是 "foo" 还是 "@types/foo"（后者对不上任何一档）。
func IsAmbientIncluded(path string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	slash := filepath.ToSlash(path)
	base := filepath.Base(slash) // 例如 foo.d.tl
	name := strings.TrimSuffix(base, ".d.tl")
	name = strings.TrimSuffix(name, ".tl")
	dirBase := filepath.Base(filepath.Dir(slash))
	for _, t := range allow {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if name == t || dirBase == t {
			return true
		}
		// 常规布局：.../@types/<t>/...、.../types/<t>...
		if strings.Contains(slash, "/@types/"+t+"/") || strings.Contains(slash, "/types/"+t+"/") {
			return true
		}
		if strings.HasSuffix(slash, "/@types/"+t+".d.tl") || strings.HasSuffix(slash, "/types/"+t+".d.tl") {
			return true
		}
	}
	return false
}

// Load 在 path 读取 tlua.json。
//
// 设计者视角：它是配置的“装载机”——ReadFile 读盘、Default 打底、Unmarshal 覆盖、
// 回填 Dir/Path 四步。障碍是缺字段：先 Default 再覆盖，让旧 tlua.json 缺新字段仍合法；
// 坏 JSON 直接透 err，不吞错。取舍是不校验（目录存否、字段合法性留给 Open），
// 让 Load 永远快且纯。不变量：成功则 Dir=Dir(path)、Path=path 原样；失败则 err 非 nil。
// 排查现场：若 Dir 漂移，先看传入的是相对还是绝对（本处不 Abs，原样 Dir），
// 次看调用方是否该先经 FindUp 拿绝对路径。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default(filepath.Dir(path))
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.Dir = filepath.Dir(path)
	cfg.Path = path
	return cfg, nil
}

// FindUp 从 dir 向上搜索 tlua.json。
//
// 设计者视角：它是“上楼找房本”——从 dir 逐层上楼，每层看有无 tlua.json，有即返，
// 到根（parent==abs）即空串收工。障碍是相对 dir：先 Abs 化再爬，避免 Join 后漂移；
// Abs 失败则退守原串继续爬，宁可慢不停摆。取舍是只判存在（Stat 成功即返），不读不验，
// 权限错当不存在继续上楼。不变量：返回非空恒为 Join 后的 tlua.json 路径；空即全树无配置。
// 排查现场：若子目录配置被父级劫持，先看搜索起点是否传成了父目录，次看中间层是否有误放的空 tlua.json。
func FindUp(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for {
		p := filepath.Join(abs, "tlua.json")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

// ListFiles 按 include/exclude/files 解析文件。
// 类型根贡献 ambient *.d.tl 文件，即使在 include 之外
// （类似 tsconfig typeRoots）；缺失根被忽略。调用方去重。
//
// 设计者视角：它是源码的“点名册”——Files 显式名单优先（点谁谁上，外加类型根声明）；
// 否则 Walk(RootDir) 全捡 .tl，再按 Exclude 剔、Include 留，末了拼上 typeRoots 的 .d.tl。
// 障碍有三：一是 outDir/dist/.git/node_modules 默认跳过防自吞（Exclude 为空也跳）；
// 二是 Walk 中途 err（如权限）当空跳过而非整单失败，保证点名永有交代；
// 三是 .d.tl 只经 listTypeRootDecls 追加、从不发射（声明只参审不出版）。
//
//	root(Dir+RootDir) ─Walk→ *.tl ─Exclude剔→ ─Include留→ 主名单
//	                                                     ＋ typeRoots *.d.tl
//
// 取舍是调用方去重（本处不 dedup 主名单），换多 Config 叠加灵活。不变量：返回路径恒为
// Join 后的形态（含 Dir 前缀）；缺失根静默忽略。排查现场：若文件忽多忽少，先看 Files
// 是否非空劫持（非空即无视 include），次看 Exclude 的 glob 是否误杀，末看 typeRoots 声明
// 是否被当成了待发射文件。
func (c *Config) ListFiles() ([]string, error) {
	root := filepath.Join(c.Dir, c.RootDir())
	if len(c.Files) > 0 {
		var out []string
		for _, f := range c.Files {
			p := f
			if !filepath.IsAbs(p) {
				p = filepath.Join(c.Dir, p)
			}
			out = append(out, p)
		}
		return append(out, c.listTypeRootDecls(out)...), nil
	}
	// 遍历 root 查找 .tl 文件。
	var all []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// 跳过排除目录 + 常见忽略。
			rel, _ := filepath.Rel(c.Dir, p)
			if rel == c.OutDir() || rel == "dist" || rel == ".git" || rel == "node_modules" {
				// 允许显式包含？默认保持跳过。
				if len(c.Exclude) == 0 {
					return filepath.SkipDir
				}
			}
			if matchAny(c.Exclude, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".tl") {
			return nil
		}
		rel, _ := filepath.Rel(c.Dir, p)
		if matchAny(c.Exclude, rel) {
			return nil
		}
		all = append(all, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(c.Include) > 0 {
		var out []string
		for _, f := range all {
			rel, _ := filepath.Rel(c.Dir, f)
			if matchAny(c.Include, rel) {
				out = append(out, f)
			}
		}
		return append(out, c.listTypeRootDecls(out)...), nil
	}
	return append(all, c.listTypeRootDecls(all)...), nil
}

// ListNativeAssets 列出 RootDir 与 tlua_modules 下的静态跨语言资产
// （*.lua shim、*.so/*.dylib/*.dll 共享库、*.a 静态归档），排除 outDir。
// 头文件/文档从不列出；缺失目录忽略。构建直通与 watch 触发
// 共用，使双方对输入集一致。
//
// 设计者视角：它是跨语言资产的“提货单”——lua 脚手架与原生库要直通进产物，头文件/文档
// 永不提。扫两仓（RootDir 与 tlua_modules，相同即一次），outDir 及其子孙整枝剪掉，
// 陈旧默认 dist/ 亦剪（防上次构建的产物被当源吸回），点文件与非资产名直接过。
// 障碍是大小写（.SO vs .so）：isNativeAssetName 先 ToLower 再比后缀。取舍是 seen 按
// Clean 径去重，返回仍保留原 p 形，构建与 watch 共此单即天然一致。不变量：结果永不含
// outDir 下径；缺失仓静默空。排查现场：若 .so 改了 watch 不触发，先对本单看径是否被
// outDir 前缀误剪，次看文件名是否以 "." 开头被当隐藏过滤。
func (c *Config) ListNativeAssets() ([]string, error) {
	outAbs := c.OutDir()
	if !filepath.IsAbs(outAbs) {
		outAbs = filepath.Join(c.Dir, outAbs)
	}
	outAbs = filepath.Clean(outAbs)
	var out []string
	seen := map[string]bool{}
	addWalk := func(root string) {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				clean := filepath.Clean(p)
				if clean == outAbs || strings.HasPrefix(clean+string(os.PathSeparator), outAbs+string(os.PathSeparator)) {
					return filepath.SkipDir
				}
				// 镜像 ListFiles：来自旧 outDir 的陈旧默认 `dist/`
				// 绝不能作为源资产被吸回。
				if rel, err := filepath.Rel(c.Dir, p); err == nil && rel == "dist" {
					return filepath.SkipDir
				}
				if info.Name() == ".git" || info.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(info.Name(), ".") || !isNativeAssetName(info.Name()) {
				return nil
			}
			clean := filepath.Clean(p)
			if seen[clean] {
				return nil
			}
			seen[clean] = true
			out = append(out, p)
			return nil
		})
	}
	addWalk(filepath.Join(c.Dir, c.RootDir()))
	vendor := filepath.Join(c.Dir, "tlua_modules")
	// RootDir 之外的 vendor 需要单独遍历（内部已覆盖）。
	if filepath.Clean(vendor) != filepath.Clean(filepath.Join(c.Dir, c.RootDir())) {
		addWalk(vendor)
	}
	return out, nil
}

// isNativeAssetName 报告文件名是否为原生资产（大小写不敏感的后缀判）。
//
// 设计者视角：它是提货单的“验货章”——.lua/.so/.dylib/.dll/.a 五印章，
// 先 ToLower 再比，mac 的 .DYLIB 与 win 的 .DLL 都认。取舍是纯名判定，不看内容魔数，
// 让 Walk 零 IO 即可过滤。不变量：点前缀的隐藏文件由调用方另滤，本处只看后缀。
// 排查现场：若 .a 未被直通，先看后缀是否拼成了 .lib（本处不认），次看是否误把头文件
// .h 当资产（见 bootstrap.go：.a 从不进 package.cpath）。
func isNativeAssetName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".lua") ||
		strings.HasSuffix(lower, ".so") ||
		strings.HasSuffix(lower, ".dylib") ||
		strings.HasSuffix(lower, ".dll") ||
		// 静态归档与共享库走相同的直通/vendor/watch 路径
		//（构建时链接到宿主，运行时 ffi.C）。
		// 它们从不进入 package.cpath（见 bootstrap.go）。
		strings.HasSuffix(lower, ".a")
}

// listTypeRootDecls 查找 typeRoots 下尚未列出的 ambient *.d.tl。
// 只包含声明文件（从不发射）；排除规则适用。
//
// 设计者视角：它是声明地的“补捡员”——主名单点完名，再到每块 typeRoots 草地捡 .d.tl，
// 已在名单（seen 按 Clean 径）即跳，Exclude 命中即弃。障碍是缺失草地：Walk err 当空过，
// 不让一块坏草地毁整单。取舍是 Exclude 按 ToSlash 后匹配，让 windows 反斜杠也中。
// 不变量：返回恒为新径（与 have 无交）；只含 .d.tl。排查现场：若声明重复参审，
// 先看主名单与 typeRoots 是否指了同一文件（Clean 后同径即去重），次看 Exclude 斜杠形态。
func (c *Config) listTypeRootDecls(have []string) []string {
	seen := map[string]bool{}
	for _, p := range have {
		seen[filepath.Clean(p)] = true
	}
	var out []string
	for _, root := range c.TypeRoots() {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if info.Name() == ".git" || info.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".d.tl") {
				return nil
			}
			if seen[filepath.Clean(p)] {
				return nil
			}
			if rel, err := filepath.Rel(c.Dir, p); err == nil {
				if matchAny(c.Exclude, filepath.ToSlash(rel)) {
					return nil
				}
			}
			seen[filepath.Clean(p)] = true
			out = append(out, p)
			return nil
		})
	}
	return out
}

// matchAny 报告相对路径是否命中任一 glob，是 Exclude/Include 的总闸。
//
// 设计者视角：好比安检门过一排模板——任一命中即拦（Exclude）或留（Include），
// 空模板集恒 false。障碍是斜杠形态：此处统一 ToSlash 后再比，让 windows 径也走 POSIX 模板。
// 不变量：纯只读，patterns 为空恒 false。排查现场：若排除不生效，先打印 rel 是否带 "./" 前缀
// （本处不剪，模板侧须一致），次看模板是否用了系统分隔符而 rel 已归一。
func matchAny(patterns []string, rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, pat := range patterns {
		if matchGlob(pat, rel) {
			return true
		}
	}
	return false
}

// matchGlob 支持 *、?、** 与字符类。
//
// 设计者视角：它是自研的“小 glob 引擎”——标准库 filepath.Match 不懂 `**`，
// 此处精确/前缀快道先行，含 `**` 走分段顺配，无 `**` 走分段逐段 Match。
// 障碍是 `**` 跨段：按 "**" 切片后首段定前缀锚、尾段定后缀锚，中段顺次 Index 推进，
// 首尾含 "**" 即免锚。取舍是不支持 `{a,b}` 扩展，换实现短、可测。不变量：pat/s 恒先 ToSlash。
// 排查现场：若 `src/**` 漏文件，先看 globMatch 的前后锚逻辑，次看是否误把 `*` 当 `**` 用
// （无 `**` 时段数必须相等，跨层即 false）。
func matchGlob(pat, s string) bool {
	pat = filepath.ToSlash(pat)
	// 快路径：精确或目录前缀。
	if pat == s {
		return true
	}
	return globMatch(pat, s)
}

// globMatch 是 matchGlob 的实现体（分 ** 与非 ** 双道）。
//
// 设计者视角：它是引擎的“机芯”——含 `**` 即按段顺配（prefixGlob 开锚、indexGlob 步进、
// suffixGlob 收锚），不含即按 "/" 切段逐段 pathMatch 且段数必等。障碍是模板里的 "/"：
// 跨段语义只在 `**` 道成立，单 `*` 永不出段。取舍是近似长度换算（globPrefixLen/indexGlob
// 按段跳），够点名用，不求字符级精确。不变量：纯函数，无 IO。
// 排查现场：若 `a/**/b` 误配 `a/b` 之外的层，先单步 prefix/index/suffix 三探头定位哪锚松了。
func globMatch(pat, s string) bool {
	px, sx := 0, 0
	var starPat, starStr = -1, -1
	// ** 单独处理：拆分。
	if strings.Contains(pat, "**") {
		parts := strings.Split(pat, "**")
		// 所有部分必须按顺序出现。
		pos := 0
		for i, part := range parts {
			part = strings.Trim(part, "/")
			if part == "" {
				continue
			}
			if i == 0 && !strings.HasPrefix(pat, "**") {
				// 必须匹配前缀。
				if !prefixGlob(part, s[pos:]) {
					return false
				}
				pos += globPrefixLen(part, s[pos:])
				continue
			}
			idx := indexGlob(part, s[pos:])
			if idx < 0 {
				return false
			}
			pos += idx + globPrefixLen(part, s[pos+idx:])
		}
		// 除非 pat 以 ** 结尾，最后一部分必须匹配后缀。
		if !strings.HasSuffix(pat, "**") {
			last := strings.Trim(parts[len(parts)-1], "/")
			if last != "" && !suffixGlob(last, s) {
				return false
			}
		}
		return true
	}
	// 无 **：对每段用 path.Match？回退到分段匹配。
	pSeg := strings.Split(pat, "/")
	sSeg := strings.Split(s, "/")
	if len(pSeg) != len(sSeg) {
		// 允许尾随 * 匹配？否。
		return false
	}
	for i := range pSeg {
		ok, _ := pathMatch(pSeg[i], sSeg[i])
		if !ok {
			return false
		}
	}
	_ = px
	_ = sx
	_ = starPat
	_ = starStr
	return true
}

// pathMatch 对单段套 filepath.Match（不跨分隔符）。
//
// 设计者视角：它是单格的“印章比对”——`*`/`?`/`[...]` 只在一段内生效，
// 分隔符由上层切段保证。取舍是直接复用标准库，语义与 Go 生态一致。
// 不变量：err 恒被调用方忽略（坏模板当不命中）。排查现场：若 `[` 未闭合的模板全不中，
// 先看 filepath.Match 的 ErrBadPattern 是否被吞（本处按 false 计）。
func pathMatch(pat, s string) (bool, error) {
	// filepath.Match 不考虑分隔符（单段）。
	return filepath.Match(pat, s)
}

// prefixGlob 报告 s 是否以模板段 pat 为前缀（含到下个 "/" 的段首比对）。
//
// 设计者视角：它是 `**` 顺配的“起跑枪”——首段非 `**` 开头时必须锚住前缀，
// 否则 `src/**` 也会从年久失修的 `old/` 起跑。障碍是段内通配：先整段 Match，
// 不中再退到首段（SplitN 取首）Match，兼顾 `src*.tl` 与 `src/foo` 两形态。
// 不变量：s 短于 seg 恒 false。排查现场：若前缀锚过松，先看 pat 是否以 "**" 开头
// （开头即无锚，查调用方的前缀分支）。
func prefixGlob(pat, s string) bool {
	// pat 是到下一个 / 的前缀。
	seg := pat
	if i := strings.Index(pat, "/"); i >= 0 {
		seg = pat[:i]
	}
	if len(s) < len(seg) {
		return false
	}
	ok, _ := filepath.Match(seg, s[:len(seg)])
	if !ok {
		// 尝试分段匹配。
		sSeg := strings.SplitN(s, "/", 2)[0]
		ok, _ = filepath.Match(seg, sSeg)
		return ok
	}
	return true
}

// globPrefixLen 估算 pat 前缀在 s 中消耗的长度（近似到匹配段末尾）。
//
// 设计者视角：它是顺配游标的“步幅尺”——prefix 命中后游标要跳过已耗段（含 "/")，
// 含 "/" 即跳整首段长+1，否则跳 len(pat)。障碍是通配段长≠字面长：此处按 s 侧实耗
// （首段长）而非 pat 长算，避免 `*` 把步幅算短。取舍是近似而非精确回放，够推进即可。
// 不变量：返回恒 >=0，调用方只做 pos+=。排查现场：若 `**` 中段跳过头，先看本尺是否把
// 多段并成了一段（本函数只跳首段，循环会逐段再跳）。
func globPrefixLen(pat, s string) int {
	// pat 前缀在 s 中消耗的长度（近似：到匹配段末尾）。
	if i := strings.Index(pat, "/"); i >= 0 {
		seg := pat[:i]
		sSeg := strings.SplitN(s, "/", 2)[0]
		_ = seg
		_ = sSeg
		return len(sSeg) + 1
	}
	return len(pat)
}

// indexGlob 在 s 的各段中找首个命中 pat 首段者，返回其字节偏移。
//
// 设计者视角：它是 `**` 的“跨段探照灯”——`**/b` 要在任意深度找 b，逐段 Match，
// 首中即返段起字节偏移（含前段的 len+1）。障碍是 pat 含 "/"：只取首段（Split 取 [0]）
// 作灯芯，余段留给后续循环逐段推进。取舍是找不到返 -1，调用方即判整式 false。
// 不变量：返值 >=0 恒为某段起始；-1 恒表无命中。排查现场：若深层文件总找不到，
// 先看段切分是 "/" 还是系统分隔符（上游已 ToSlash，此处只认 "/"）。
func indexGlob(pat, s string) int {
	seg := strings.Split(pat, "/")[0]
	sSegs := strings.Split(s, "/")
	for i, ss := range sSegs {
		ok, _ := filepath.Match(seg, ss)
		if ok {
			// 计算字节偏移。
			off := 0
			for j := 0; j < i; j++ {
				off += len(sSegs[j]) + 1
			}
			return off
		}
	}
	return -1
}

// suffixGlob 报告 s 的末段是否命中 pat 的末段（`**` 尾锚）。
//
// 设计者视角：它是顺配的“终点线”——pat 非 `**` 结尾时末段必须锚住（如 `*.tl`），
// 否则 `src/**.tl` 会把 `src/a/bak` 也收进来。各取末段（LastIndex "/" 后）再 Match，
// 让 `a/**/b` 的 b 只认末名。取舍是只看末段，中间段由 index 循环兜底。
// 不变量：纯段尾比对，不看前段。排查现场：若 `**/x` 收到 `ax`，先看末段模板是否缺分隔
// （`*.tl` 与 `**.tl` 语义不同），次看 Match 的 `*` 是否跨了段（本处已限单段）。
func suffixGlob(pat, s string) bool {
	seg := pat
	if i := strings.LastIndex(pat, "/"); i >= 0 {
		seg = pat[i+1:]
	}
	sSeg := s
	if i := strings.LastIndex(s, "/"); i >= 0 {
		sSeg = s[i+1:]
	}
	ok, _ := filepath.Match(seg, sSeg)
	return ok
}
