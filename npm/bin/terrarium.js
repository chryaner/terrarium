#!/usr/bin/env node
'use strict';

// Runs the bundled terrarium.exe on this process's stdio, so `npx terrarium-mcp
// mcp` is the MCP server itself. Nothing may be written to stdout here: the
// MCP transport owns it, and a stray byte breaks the client's framing.
const { spawn } = require('child_process');
const path = require('path');

if (process.platform !== 'win32') {
  process.stderr.write('terrarium runs on a Windows host with VirtualBox; this platform is not supported\n');
  process.exit(1);
}

const exe = path.join(__dirname, '..', 'dist', 'terrarium.exe');
const child = spawn(exe, process.argv.slice(2), { stdio: 'inherit', windowsHide: true });

child.on('error', (err) => {
  process.stderr.write('terrarium: ' + err.message + '\n');
  process.exit(1);
});
child.on('exit', (code) => {
  process.exit(code === null ? 1 : code);
});
for (const sig of ['SIGINT', 'SIGTERM']) {
  process.on(sig, () => child.kill(sig));
}
