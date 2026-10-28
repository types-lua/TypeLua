// Package bindgen 从 C 头文件子集生成 TypeLua 的 .d.tl 类型契约（FFI 跨语言契约）。
//
// 它是“总而全的防御性”转换器：未知构造从不报错，一律退化为 `any` 并附
// `-- skipped` 注释，因此输出永远能被 TypeLua 解析。目标是“不产生假阳性”，
// 而不是“穷尽 C 语言”。
//
// # 为什么把 C 头文件翻译成 .d.tl 而不是直接调用
//
// TypeLua 的类型系统只认识 .d.tl 里的声明；bindgen 是把 C ABI 的形状翻译
// 成 TypeLua 能检查的形状，让 `ffi.load` 的结果有类型可查。指针/数组除
// `char*` 外一律 `cdata`，未知按值类型一律 `any`，宁可少报错也不误报。
//
// # 转换策略
//
//   - typedef struct/struct/enum/alias、函数原型、#define、函数指针 typedef
//     各自映射为 TypeLua 声明；
//   - 输出确定性：struct/enum/alias 按名字排序，方法保持源序；
//   - 未知一律 `any` + `-- skipped`，保证输出可解析。
//
// 明确不做
//   - 不编译/链接 C，不生成运行时绑定代码（只生成类型契约）；
//   - 不支持完整 C 预处理器与复杂宏；
//   - 不做语义分析或类型推断，仅做语法形状翻译。
//
// 不变量
//   - Generate 对任意输入都不 panic，输出始终是合法 .d.tl；
//   - 未知构造退化为 `any`，从不导致下游解析失败；
//   - 结构/枚举/别名输出顺序确定。
//
// 模块间协作
//   - 依赖：无内部包（仅标准库）；测试使用 parser 验证输出可解析。
//   - 被依赖：cmd/tlua（bindgen 子命令）。
package bindgen
