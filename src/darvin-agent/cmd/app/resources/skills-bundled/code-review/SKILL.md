---
name: code-review
description: 对代码做静态审查 + 给出修改建议
version: 0.1.0
invocation:
  userInvocable: true
  disableModelInvocation: false
---

# Code Review Skill

执行多文件静态代码审查并给出修改建议。

适用场景：

- 用户要求对 git diff、目录或文件做 review
- 与 `git status` / `git diff` 配合
- 输出可粘贴的 review 报告

约束：

- 不修改任何文件
- 引用具体行号
- 按风险级别排序
