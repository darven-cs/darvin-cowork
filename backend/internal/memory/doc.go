// Package memory 实现四层记忆系统。
//
// 四层结构（详见 docs/v0.1.md §2.4）：
//   - SOUL    : Agent 的核心人设与长期偏好
//   - USER    : 用户画像、跨会话稳定事实
//   - MEMORY  : 向量化的语义记忆（按相关性召回）
//   - 会话    : 当前会话的临时上下文窗口
//
// v0.1 排期：
//   - 第 3 周：SOUL + USER 静态加载，会话层走内存缓冲
//   - 第 4 周：MEMORY 接入向量库（modernc.org/sqlite + 向量扩展）
//
// 详见 docs/v0.1.md §2.4；docs/lobsterai-borrowed-patterns.md §4。
package memory
