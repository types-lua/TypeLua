// Package bindgen 从 C 头文件子集生成 TypeLua 的 .d.tl 类型契约（FFI 跨语言契约）。
//
// 它是“总而全的防御性”转换器：未知构造从不报错，一律退化为 `any` 并附
// `-- skipped` 注释，因此输出永远能被 TypeLua 解析。目标是“不产生假阳性”，
// 而不是“穷尽 C 语言”。
//
// # 为什么把 C 头文件翻译成 .d.tl 而不是直接调用
// …（略，详见实现；排查先看 Span/诊断）
// 模块间协作
//   - 依赖：无内部包（仅标准库）；测试使用 parser 验证输出可解析。
//   - 被依赖：cmd/tlua（bindgen 子命令）。
package bindgen
