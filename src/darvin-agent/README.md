# darvin-agent

Go 编写的 agent runtime，作为 Electron 主进程的子进程运行。

## 目录结构（占位）

```
darvin-agent/
├── go.mod
├── main.go
└── ...
```

## 模块名

待定，参见 `specs/refactors/electron-webpack-to-electron-forge-vite/2026-07-27-webpack-to-electron-forge-vite-design.md` 4.7 节。

## 构建

仓库根目录：

```bash
npm run build:agent       # 当前平台
npm run make              # premake 钩子自动触发 build:agent，再 make 安装包
```

## 通信协议

待定。`src/main/runtime/client.ts` 占位。
