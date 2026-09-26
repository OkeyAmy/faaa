#!/usr/bin/env node
// Thin launcher: exec the native binary. Git hooks call the binary directly,
// so node never sits on the git push path.
const { spawnSync } = require("child_process");
const path = require("path");
const exe = path.join(__dirname, "bin", process.platform === "win32" ? "faaa.exe" : "faaa");
const r = spawnSync(exe, process.argv.slice(2), { stdio: "inherit" });
if (r.error) { console.error("faaa:", r.error.message, "(try reinstalling)"); process.exit(1); }
process.exit(r.status ?? 0);
