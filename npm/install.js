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
const url = `https://github.com/OkeyAmy/faaa/releases/download/v${version}/faaa_${plat}_${arch}.${ext}`;
const bin = path.join(__dirname, "bin");
const archive = path.join(os.tmpdir(), `faaa-${process.pid}.${ext}`);

function get(u, cb) {
  https.get(u, { headers: { "User-Agent": "faaa-npm" } }, (r) => {
    if (r.statusCode >= 300 && r.statusCode < 400 && r.headers.location) return get(r.headers.location, cb);
    if (r.statusCode !== 200) { console.error(`faaa: download failed ${r.statusCode} ${u}`); process.exit(1); }
    r.pipe(fs.createWriteStream(archive)).on("finish", cb);
  }).on("error", (e) => { console.error("faaa:", e.message); process.exit(1); });
}

get(url, () => {
  fs.mkdirSync(bin, { recursive: true });
  // bsdtar ships with macOS and Windows 10+, GNU tar with Linux; both read zip via -a/-x on Windows.
  execFileSync("tar", ["-xf", archive, "-C", bin]);
  fs.unlinkSync(archive);
  if (plat !== "windows") fs.chmodSync(path.join(bin, "faaa"), 0o755);
});
