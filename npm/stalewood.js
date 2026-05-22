#!/usr/bin/env node
"use strict";
// Entry point of the `stalewood` npm package. The real binary ships in a
// per-platform optional-dependency package; this shim locates and runs it.
const { execFileSync } = require("node:child_process");

const pkg = `stalewood-${process.platform}-${process.arch}`;
const exe = process.platform === "win32" ? "stalewood.exe" : "stalewood";

let bin;
try {
	bin = require.resolve(`${pkg}/bin/${exe}`);
} catch {
	console.error(
		`stalewood: no prebuilt binary for ${process.platform}-${process.arch}.`,
	);
	console.error("See https://github.com/retif/stalewood/releases");
	process.exit(1);
}

try {
	execFileSync(bin, process.argv.slice(2), { stdio: "inherit" });
} catch (err) {
	process.exit(typeof err.status === "number" ? err.status : 1);
}
