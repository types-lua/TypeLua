// Package compiler 编排 TypeLua 的完整编译流水线：加载、解析、检查、发射与诊断聚合。
//
// 它是“厂长”：读取配置与文件列表，按依赖顺序驱动 parser/checker/emitter，
// 收集各阶段诊断，并支持增量、并行与 tree-shaking。声明文件（*.d.tl）只检查
// 不发货，普通文件检查并发货。
//
// # 为什么把调度集中在一个 Program
//
// LevelDB 式的教训：状态转换散在各包里，任一转换都要跨包协调，容易出现
// “半版本/半批次”。Program 把“谁在什么时候调用谁”集中起来，让加载顺序、
// 诊断聚合、增量失效可审计，而不是追求包内算法极致。算法留在 parser/
// checker/emitter，调度留在这里。
//
// # 核心对象
//
//   - SourceFile：一个加载单元（路径、内容、AST、是否声明文件）；
//   - Program：文件集合 + 诊断 + 配置 + 模块短名映射；
//   - Load/LoadSources：并行读取与解析，结果按路径排序，保证确定性；
//   - Check：按依赖顺序检查，未解析的 require 退化为 `any`；
//   - Emit：发射 Lua 与 source map，可选 tree-shaking。
//
// # 为什么声明文件不发货
//
// *.d.tl 是类型契约，只参与检查（ambient scope），不产生 Lua 输出——
// 这是 TypeScript 的 .d.ts 语义在 TypeLua 的对应。
//
// 明确不做
//   - 不实现词法/语法/类型/发射算法（分别由对应包负责）；
//   - 不做文件监听（由 watch 包负责）；
//   - 不做包管理下载（由 mod 包负责）。
//
// 不变量
//   - Files 与 Diags 按路径排序，保证并行加载结果确定；
//   - 声明文件 IsDecl=true，不进入发射列表；
//   - 未解析的 require 退化为 `any`，不中断检查；
//   - 诊断聚合后由 FormatDiags 按“文件-行号”排序。
//
// 模块间协作
//   - 依赖：ast、checker、config、diag、emitter、parser、sourcemap、mod、
//     watch、types。
//   - 被依赖：cmd/tlua、watch。
package compiler
