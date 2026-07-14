import { BrowserWindow, app, session } from 'electron';
import { createWindow } from './window';
import { registerIpc } from './ipc';
import { killGo, startGo, waitForGo } from './go';

function configureProxy(): void {
  const isDev = !app.isPackaged;
  session.defaultSession
    .setProxy({ mode: isDev ? 'direct' : 'system' })
    .then(() => console.log(`[main] proxy mode: ${isDev ? 'direct' : 'system'}`))
    .catch((err) => console.error('[main] setProxy failed:', err));
}

app.whenReady().then(async () => {
  configureProxy();
  startGo();

  try {
    await waitForGo();
  } catch (e) {
    console.error('[main]', (e as Error).message);
    killGo();
    app.quit();
    return;
  }

  registerIpc();
  createWindow();

  app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
  });

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on('before-quit', killGo);
app.on('will-quit', killGo);
process.on('exit', killGo);
