#!/usr/bin/env node
'use strict';

// Runs the bundled terrarium binary on this process's stdio, so `npx
// terrarium-mcp mcp` is the MCP server itself. Nothing may be written to
// stdout here: the MCP transport owns it, and a stray byte breaks the client's
// framing.
const { spawn } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const binaries = {
  'win32-x64': 'terrarium.exe',
  'linux-x64': 'terrarium-linux-x64',
};

function fail(msg) {
  process.stderr.write('terrarium: ' + msg + '\n');
  process.exit(1);
}

// A package packed on Windows carries no execute bit. Restore it in place, or,
// where the install is read-only to this user, run a private copy.
function executable(file) {
  if (process.platform === 'win32') return file;
  try {
    fs.accessSync(file, fs.constants.X_OK);
    return file;
  } catch (_) {}
  try {
    fs.chmodSync(file, 0o755);
    return file;
  } catch (_) {}
  // The user's own cache, not a shared temp directory another user could
  // plant a binary in first.
  const version = require('../package.json').version;
  const cache = process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache');
  const copy = path.join(cache, 'terrarium-mcp', version, path.basename(file));
  try {
    fs.accessSync(copy, fs.constants.X_OK);
    return copy;
  } catch (_) {}
  fs.mkdirSync(path.dirname(copy), { recursive: true, mode: 0o700 });
  const tmp = copy + '.' + process.pid;
  fs.copyFileSync(file, tmp);
  fs.chmodSync(tmp, 0o755);
  fs.renameSync(tmp, copy);
  return copy;
}

const name = binaries[process.platform + '-' + process.arch];
if (!name) {
  fail('no binary for ' + process.platform + '-' + process.arch + '; terrarium runs on x64 Windows and Linux hosts with VirtualBox');
}

let exe;
try {
  exe = executable(path.join(__dirname, '..', 'dist', name));
} catch (err) {
  fail('cannot make the bundled binary executable: ' + err.message);
}

const child = spawn(exe, process.argv.slice(2), { stdio: 'inherit', windowsHide: true });

child.on('error', (err) => fail(err.message));
child.on('exit', (code) => {
  process.exit(code === null ? 1 : code);
});
for (const sig of ['SIGINT', 'SIGTERM']) {
  process.on(sig, () => child.kill(sig));
}
