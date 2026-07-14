const { app, BrowserWindow, session } = require('electron');
const path = require('path');
const { spawn } = require('child_process');

// ============== 全局状态 ==============
let goProcess = null;
let mainWindow = null;

// ============== 0. 代理设置 ==============
function configureProxy() {
  const isDev = !!process.env.ELECTRON_START_URL;
  session.defaultSession
    .setProxy({ mode: isDev ? 'direct' : 'system' })
    .then(() => console.log(`[main] proxy mode: ${isDev ? 'direct' : 'system'}`))
    .catch((err) => console.error('[main] setProxy failed:', err));
}

// ============== 1. 启动 Go 后端 ==============
function startGo() {
  const backendDir = path.join(__dirname, '..', 'backend');

  goProcess = spawn('go', ['run', './cmd'], {
    cwd: backendDir,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  goProcess.stdout.on('data', (d) => console.log('[go]', d.toString().trim()));
  goProcess.stderr.on('data', (d) => console.error('[go!]', d.toString().trim()));
  goProcess.on('exit', (code) => {
    console.log(`[go] exited with code ${code}`);
    goProcess = null;
  });
}

// ============== 2. 等 Go 健康 ==============
async function waitForGo(retries = 100) {
  const url = 'http://127.0.0.1:8080/api/hello';
  for (let i = 0; i < retries; i++) {
    try {
      const r = await fetch(url);
      if (r.ok) {
        console.log('[main] Go backend ready');
        return;
      }
    } catch {}
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error('Go backend failed to start in 20s');
}

// ============== 3. 创建窗口 ==============
function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1024,
    height: 720,
    title: 'Darvin Cowork',
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  // 开发期走 Vite dev server（HMR），生产期走打包好的文件
  const devUrl = process.env.ELECTRON_START_URL;
  if (devUrl) {
    console.log('[main] loading dev URL:', devUrl);
    mainWindow.loadURL(devUrl);
  } else {
    mainWindow.loadFile(path.join(__dirname, 'dist-react', 'index.html'));
  }

  // 开发期按 F12 打开 DevTools
  if (!app.isPackaged) {
    mainWindow.webContents.on('did-finish-load', () => {
      mainWindow.webContents.openDevTools({ mode: 'detach' });
    });
  }
}

// ============== 4. 生命周期 ==============
app.whenReady().then(async () => {
  configureProxy();
  startGo();

  try {
    await waitForGo();
  } catch (e) {
    console.error('[main]', e.message);
    if (goProcess) goProcess.kill();
    app.quit();
    return;
  }

  createWindow();

  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
  });

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on('before-quit', () => {
  if (goProcess) {
    console.log('[main] killing Go process...');
    goProcess.kill();
    goProcess = null;
  }
});

app.on('will-quit', () => {
  if (goProcess) {
    goProcess.kill();
    goProcess = null;
  }
});

// 最后兜底
process.on('exit', () => {
  if (goProcess) goProcess.kill();
});
