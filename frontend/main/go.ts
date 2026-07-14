import { type ChildProcess, spawn } from 'node:child_process';
import { join } from 'node:path';

let goProcess: ChildProcess | null = null;

export function startGo(): void {
  const backendDir = join(__dirname, '../../../backend');
  goProcess = spawn('go', ['run', './cmd/server'], {
    cwd: backendDir,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  goProcess.stdout?.on('data', (d) => console.log('[go]', d.toString().trim()));
  goProcess.stderr?.on('data', (d) => console.error('[go!]', d.toString().trim()));
  goProcess.on('exit', (code) => {
    console.log(`[go] exited with code ${code}`);
    goProcess = null;
  });
}

export async function waitForGo(retries = 100): Promise<void> {
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

export function killGo(): void {
  if (goProcess) {
    console.log('[main] killing Go process...');
    goProcess.kill();
    goProcess = null;
  }
}
