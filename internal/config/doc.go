// Package config 定义 tlua.json 的 schema 与编译选项的有效值（含默认值）。
//
// Config 是纯数据结构：它描述配置，不校验、不装配。校验与装配属于 compiler/
// Program 与打开流程的职责，因为只有打开时才知道目录是否存在、comparator
// 名字是否对得上。把校验塞进 Config 会让“同一个 Config 对象”在不同打开
// 场景下语义不同，且构造 Config 的单元测试得连带构造半个编译器。
//
// …（略，详见实现；排查先看 Span/诊断）
// 模块间协作
//   - 依赖：无内部包（仅标准库）。
//   - 被依赖：compiler、watch、cmd/tlua。
package config
