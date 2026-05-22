// Builds per-platform npm packages from the Go source and publishes them:
// six `stalewood-<os>-<cpu>` packages carrying the prebuilt binary, plus the
// main `stalewood` package that selects the right one via optionalDependencies.
// Run by .github/workflows/release.yml. Set NPM_DRY_RUN=1 for a local check.
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync, copyFileSync, readFileSync } from "node:fs";
import { join } from "node:path";

const root = new URL("..", import.meta.url).pathname;
const version = readFileSync(join(root, "VERSION"), "utf8").trim();
const dryRun = !!process.env.NPM_DRY_RUN;
// Provenance needs CI OIDC; NPM_NO_PROVENANCE=1 drops it for local publishes.
const withProvenance = !process.env.NPM_NO_PROVENANCE;
const publishArgs = dryRun
  ? ["--dry-run", "--ignore-scripts"]
  : [
      "--access",
      "public",
      "--ignore-scripts",
      ...(withProvenance ? ["--provenance"] : []),
    ];
const repository = {
  type: "git",
  url: "git+https://github.com/retif/stalewood.git",
};

const platforms = [
  { goos: "linux", goarch: "amd64", os: "linux", cpu: "x64" },
  { goos: "linux", goarch: "arm64", os: "linux", cpu: "arm64" },
  { goos: "darwin", goarch: "amd64", os: "darwin", cpu: "x64" },
  { goos: "darwin", goarch: "arm64", os: "darwin", cpu: "arm64" },
  { goos: "windows", goarch: "amd64", os: "win32", cpu: "x64" },
  { goos: "windows", goarch: "arm64", os: "win32", cpu: "arm64" },
];

const dist = join(root, "npm-dist");
const optionalDependencies = {};

for (const p of platforms) {
  const pkg = `@retif/stalewood-${p.os}-${p.cpu}`;
  const exe = p.goos === "windows" ? "stalewood.exe" : "stalewood";
  const dir = join(dist, pkg);
  mkdirSync(join(dir, "bin"), { recursive: true });
  console.log(`building ${pkg} (v${version})`);
  execFileSync(
    "go",
    [
      "build",
      "-trimpath",
      "-ldflags",
      "-s -w",
      "-o",
      join(dir, "bin", exe),
      ".",
    ],
    {
      cwd: root,
      stdio: "inherit",
      env: { ...process.env, GOOS: p.goos, GOARCH: p.goarch, CGO_ENABLED: "0" },
    },
  );
  writeFileSync(
    join(dir, "package.json"),
    JSON.stringify(
      {
        name: pkg,
        version,
        description:
          `stalewood binary for ${p.os}-${p.cpu} — installed automatically by ` +
          `the 'stalewood' package; do not install directly`,
        license: "MIT",
        repository,
        homepage: "https://www.npmjs.com/package/stalewood",
        os: [p.os],
        cpu: [p.cpu],
        files: ["bin/"],
      },
      null,
      2,
    ) + "\n",
  );
  writeFileSync(
    join(dir, "README.md"),
    `# ${pkg}\n\n` +
      `Prebuilt \`stalewood\` binary for \`${p.os}-${p.cpu}\`.\n\n` +
      `Installed automatically as an optional dependency of the\n` +
      `[**stalewood**](https://www.npmjs.com/package/stalewood) package — do not\n` +
      `install it directly. Source: <https://github.com/retif/stalewood>.\n`,
  );
  execFileSync("npm", ["publish", dir, ...publishArgs], { stdio: "inherit" });
  optionalDependencies[pkg] = version;
}

const main = join(dist, "stalewood");
mkdirSync(join(main, "bin"), { recursive: true });
copyFileSync(
  join(root, "npm", "stalewood.js"),
  join(main, "bin", "stalewood.js"),
);
copyFileSync(join(root, "README.md"), join(main, "README.md"));
copyFileSync(join(root, "LICENSE"), join(main, "LICENSE"));
writeFileSync(
  join(main, "package.json"),
  JSON.stringify(
    {
      name: "stalewood",
      version,
      description: "Find and reap merged git worktrees",
      license: "MIT",
      repository,
      homepage: "https://github.com/retif/stalewood",
      bin: { stalewood: "bin/stalewood.js" },
      files: ["bin/"],
      optionalDependencies,
    },
    null,
    2,
  ) + "\n",
);
console.log(`publishing stalewood (v${version})`);
execFileSync("npm", ["publish", main, ...publishArgs], { stdio: "inherit" });
