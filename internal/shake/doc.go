// Package shake 对 TypeLua AST 做保守的 tree-shaking：删除零引用的纯局部函数。
//
// 只删 `local function f` / `local x = function` 这类纯闭包定义；其它一切
// 永远保留。安全契约的偏置是“保留”：宁可少删，绝不删错。
//
// # 为什么只动纯局部函数
//
// …（略，详见实现；排查先看 Span/诊断）
// 模块间协作
//   - 依赖：ast。
//   - 被依赖：emitter（可选 TreeShake 在并发检测前运行）。
package shake
