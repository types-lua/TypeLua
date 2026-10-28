package diag

import (
	"fmt"

	"github.com/typelua/typelua/internal/token"
	"github.com/typelua/typelua/internal/types"
)

// 诊断的严重度。
//
// 设计者视角：Severity 是整改单上的“红/黄章”——Error 拦路（编译失败），Warning 放行
// （照常发射）。障碍是后层（CLI/LSP/测试）要按级过滤：用 int 枚举而非布尔，留出未来
// 扩展 Note/Hint 的余地。不变量：零值为 Error，让漏填严重度的诊断默认最严，宁严勿松。
type Severity int

const (
	// Error 表示错误级别诊断。
	//
	// 设计者视角：它是默认章——零值为 Error，让漏填 Severity 的诊断自动拦路，
	// 避免“本该报错却放行”的漏网。不变量：String() 印为 "error"。
	// 排查现场：若本应警告的被当错误中断构建，先看构造是否误用 Errorf 而非 New(Warning)。
	Error Severity = iota
	// Warning 表示警告级别诊断。
	//
	// 设计者视角：它是黄章——照常发射，只记一笔供 CLI/LSP 展示，测试按码断言时
	// 常需区分 Warning 与 Error。取舍是仅两档，不设 Note/Hint，让过滤逻辑保持一刀切。
	Warning
)

// String 返回 Severity 的文本形式（error/warning）。
//
// 设计者视角：它是章印的“拓印”——Warning 拓为 "warning"，其余一律兜底为 "error"，
// 宁严勿松，让未知值默认拦路而非悄悄放行。取舍是用 if 而非 switch，使未来新增级别
// 在未更新此处前仍落回 error 而非空串。不变量：永不返回空串。排查现场：若日志里
// severity 忽隐忽现，先看是否直接打印了 int 而非调本函数。
func (s Severity) String() string {
	if s == Warning {
		return "warning"
	}
	return "error"
}

// Diagnostic 是一条编译器消息：稳定 Code + 人读 Message + 位置 Span + 严重度。
//
// 物理布局：
//
//	Diagnostic
//	├── Code     Code           // 稳定编号 TLnnnn
//	├── Message  string         // 人读解释
//	├── Span     token.Span     // 文件/行列（半开区间）
//	└── Severity Severity       // Error/Warning
//
// 设计者视角：Diagnostic 是发给用户的“整改单”——Code 供机器匹配（IDE/测试/脚本按码断言），
// Message 供人阅读（措辞可改，码不可变），Span 供编辑器画线，Severity 定拦路还是放行。
// 障碍是文本会漂移而工具需稳定：把“稳定标识”与“人话解释”拆成两字段，改措辞不碎脚本。
// 取舍是不持源文本、不做渲染，渲染/着色留给 CLI/LSP，诊断保持纯数据可排序可聚合。
// 不变量：Code 恒符合 TLnnnn 形状；Span.File 为空即无定位。排查现场：若按文本匹配测试
// 频繁碎裂，改按 Code 断言；若跨文件排序错乱，先查 Span.File 是否漏填。
type Diagnostic struct {
	Code     Code
	Message  string
	Span     token.Span
	Severity Severity
}

// Error 实现 error 接口，返回 String()。
//
// 设计者视角：它是整改单进 Go 错误世界的“转接头”——好比让 Diagnostic 能直接塞进
// err 聚合、fmt.Errorf 的 %w 链，多诊断返回无需再包一层。取舍是值接收者，拷贝四字段
// 开销可忽略，换所有权清晰。不变量：Error() 与 String() 输出恒一致。
// 排查现场：若 errors.As 拆不出 Diagnostic，先确认是否被 fmt 包成了新串而丢了结构。
func (d Diagnostic) Error() string { return d.String() }

// String 渲染为 "file:line:col severity[code]: message"。
//
// 设计者视角：它是整改单的“打印行”——好比 `main.tl:3:5: error[TL3014]: undefined variable`，
// 人眼一行即定位，机器按 `[TLxxxx]` 正则即抽码。障碍有二：一是类型串可深达数千层，
// 直接打印会冲垮终端，故经 types.Abbrev 按括号深度折叠，浅层消息字节不变；
// 二是无文件诊断（如测试合成）无前缀可挂，File 为空即省掉定位段。取舍是渲染只做纯文本，
// 着色/多行源码摘录留给上层。不变量：恒含 severity 与 [code]，Message 侧永经 Abbrev。
// 排查现场：若单测比对诊断串失败，先看 Abbrev 是否折叠了深类型，改按 Code+前缀断言。
func (d Diagnostic) String() string {
	loc := ""
	if d.Span.File != "" {
		loc = fmt.Sprintf("%s:%s: ", d.Span.File, d.Span.Start)
	}
	// 超深类型折叠（可读性；浅层消息相同）。
	return fmt.Sprintf("%s%s[%s]: %s", loc, d.Severity, d.Code, types.Abbrev(d.Message))
}

// 构造诊断的辅助函数；错误码来自 codes.go 的注册表。
//
// 设计者视角：下两函数是记账的“复写纸”——调用点只给码、位、格式串，Sprintf 与结构组装
// 收敛一处，避免各处手拼 Message 导致格式漂移。不变量：构造永不校验 Code 是否注册
// （注册检查留给测试与 Category()），保证热点零负担。

// New 构造一条诊断。
//
// 设计者视角：它是全级别通用章——sev 由调用方明示（Warning 显式传），format/args 在此
// 一次 Sprintf 定稿。取舍是不截断、不去重，去重由 compiler 聚合层做，此处只管忠实记录。
// 不变量：Message 恒为格式化后的终稿，后续 Abbrev 只在 String() 时现算。
// 排查现场：若诊断里出现 `%!v(MISSING)`，先查 format 动词与 args 个数是否对齐。
func New(code Code, span token.Span, sev Severity, format string, args ...any) Diagnostic {
	return Diagnostic{Code: code, Span: span, Severity: sev, Message: fmt.Sprintf(format, args...)}
}

// Errorf 构造一条 Error 级别诊断。
//
// 设计者视角：它是九成调用点的“快捷红章”——Error 占绝大多数，省去每次手传 sev 的噪音，
// 让 `diag.Errorf(diag.LexInvalidCharacter, sp, ...)` 一行即记账。取舍是 Warning 仍走 New
// 明示级别，避免默认红章误拦。不变量：返回的 Severity 恒为 Error。
// 排查现场：若警告被当错误中断，先看是否误用了本函数而非 New(..., Warning, ...)。
func Errorf(code Code, span token.Span, format string, args ...any) Diagnostic {
	return New(code, span, Error, format, args...)
}
