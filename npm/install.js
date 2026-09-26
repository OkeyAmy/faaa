// Downloads the prebuilt faaa binary for this platform. No dependencies.
const { execFileSync } = require("child_process");
const fs = require("fs");
const https = require("https");
const os = require("os");
const path = require("path");

const { version } = require("./package.json");
const plat = { linux: "linux", darwin: "darwin", win32: "windows" }[process.platform];
const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
if (!plat || !arch) {
  console.error(`faaa: unsupported platform ${process.platform}/${process.arch}`);
  process.exit(1);
}
const ext = plat === "windows" ? "zip" : "tar.gz";
const file = `faaa_${plat}_${arch}.${ext}`;
const base = "https://github.com/OkeyAmy/faaa/releases";
// exact release for this package version; fall back to latest if it's missing
const urls = [`${base}/download/v${version}/${file}`, `${base}/latest/download/${file}`];
const bin = path.join(__dirname, "bin");
const archive = path.join(os.tmpdir(), `faaa-${process.pid}.${ext}`);

function get(u, cb, next) {
  https.get(u, { headers: { "User-Agent": "faaa-npm" } }, (r) => {
    if (r.statusCode >= 300 && r.statusCode < 400 && r.headers.location) return get(r.headers.location, cb, next);
    if (r.statusCode !== 200) { r.resume(); return next(`${r.statusCode} ${u}`); }
    r.pipe(fs.createWriteStream(archive)).on("finish", cb);
  }).on("error", (e) => next(e.code || e.message || String(e)));
}

function tryUrls(i, cb) {
  get(urls[i], cb, (why) => {
    if (i + 1 < urls.length) return tryUrls(i + 1, cb);
    console.error(`faaa: download failed: ${why}`);
    process.exit(1);
  });
}

tryUrls(0, () => {
  fs.mkdirSync(bin, { recursive: true });
  // bsdtar ships with macOS and Windows 10+, GNU tar with Linux; both read zip via -a/-x on Windows.
  execFileSync("tar", ["-xf", archive, "-C", bin]);
  fs.unlinkSync(archive);
  if (plat !== "windows") fs.chmodSync(path.join(bin, "faaa"), 0o755);
});
