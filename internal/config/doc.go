// Package config 定义 tlua.json 的 schema 与编译选项的有效值（含默认值）。
//
// Config 是纯数据结构：它描述配置，不校验、不装配。校验与装配属于 compiler/
// Program 与打开流程的职责，因为只有打开时才知道目录是否存在、comparator
// 名字是否对得上。把校验塞进 Config 会让“同一个 Config 对象”在不同打开
// 场景下语义不同，且构造 Config 的单元测试得连带构造半个编译器。
//
// # 为什么配置是纯数据
//
// 纯数据让配置可复制、可 diff、可序列化；代价是调用方可能填出非法组合——
// 由打开/编译流程兜底报错。0 值通常表示“用默认”，由访问方法解释。
//
// # 有效值与默认值
//
// Target 默认 "5.4"；Strict 默认 true；OutDir 默认 "dist"；RootDir 默认 "."。
// TypeRoots/AmbientTypes 镜像 tsconfig 的 typeRoots/types 语义。
//
// 明确不做
//   - 不做合法性校验（compiler/Open 负责）；
//   - 不持有运行时状态；
//   - 不决定算法策略（只给参数）。
//
// 不变量
//   - 0 值表示“用默认”，由方法解释；
//   - CompilerOptions 中带 json:"-" 的 Dir/Path 不参与序列化；
//   - Ambient allowlist 为空表示包含全部 ambient .d.tl。
//
// 模块间协作
//   - 依赖：无内部包（仅标准库）。
//   - 被依赖：compiler、watch、cmd/tlua。
package config
