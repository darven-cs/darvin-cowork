#!/usr/bin/env node
/* eslint-disable no-console */
// scripts/build-go.js
// 跨平台编译 darvin-agent；输出到 <repo>/bin/darvin-agent-<platform>-<arch><ext>
// 由 premake 钩子自动调用。

const { execSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const platform = process.platform; // 'darwin' | 'linux' | 'win32'
const arch = process.arch; // 'arm64' | 'x64'
const exeSuffix = platform === 'win32' ? '.exe' : '';
const outName = `darvin-agent-${platform}-${arch}${exeSuffix}`;
const repoRoot = path.join(__dirname, '..');
const outPath = path.join(repoRoot, 'bin', outName);

const agentDir = path.join(repoRoot, 'src', 'darvin-agent');
const env = { ...process.env, CGO_ENABLED: '0', GOOS: platform, GOARCH: arch };

if (!fs.existsSync(agentDir)) {
  console.error(`[build-go] darvin-agent 源码目录不存在: ${agentDir}`);
  console.error('[build-go] 跳过编译（go 业务逻辑尚未实现，骨架阶段）');
  process.exit(0);
}

fs.mkdirSync(path.dirname(outPath), { recursive: true });

try {
  execSync(`go build -ldflags="-s -w" -o "${outPath}" .`, {
    cwd: agentDir,
    env,
    stdio: 'inherit',
  });
  console.log(`[build-go] ✓ built ${outPath}`);
} catch (err) {
  console.error(`[build-go] ✗ build failed: ${err.message}`);
  process.exit(err.status || 1);
}
