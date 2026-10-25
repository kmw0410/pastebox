const { spawnSync, spawn } = require('node:child_process');
const { mkdtempSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join, resolve } = require('node:path');
const root = resolve(__dirname, '../..');
const temp = mkdtempSync(join(tmpdir(), 'pastebox-browser-'));
const binary = join(temp, 'pastebox');
const build = spawnSync('go', ['build', '-mod=readonly', '-o', binary, './cmd/server'], { cwd: root, stdio: 'inherit' });
if (build.status !== 0) { rmSync(temp, { recursive: true, force: true }); process.exit(1); }
const child = spawn(binary, [], {
  cwd: root,
  stdio: 'inherit',
  env: { ...process.env, LISTEN_ADDR: '127.0.0.1:18080', DATA_DIR: join(temp, 'data'), LANGUAGE: 'en', STORAGE_BACKEND: 'local', DISCORD_WEBHOOK: '', ADMIN_RESET_TOKEN: '' },
});
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => child.kill(signal));
child.on('exit', code => { rmSync(temp, { recursive: true, force: true }); process.exit(code ?? 0); });
