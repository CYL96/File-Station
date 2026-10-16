#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  printf '错误：用法：bash scripts/release-version.sh TAG\n' >&2
  exit 1
fi

tag=$1
if [[ ! $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  printf '错误：发布标签必须为正式版本 vMAJOR.MINOR.PATCH，且版本段不能有前导零：%s\n' "$tag" >&2
  exit 1
fi

if ! command -v node >/dev/null 2>&1; then
  printf '错误：发布版本校验需要 Node.js，请先安装 Node.js。\n' >&2
  exit 1
fi

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
node --input-type=commonjs - "$repo_root" "$tag" <<'NODE'
const fs = require('node:fs');
const path = require('node:path');
const [root, tag] = process.argv.slice(2);

try {
  const expected = tag.slice(1);
  const canonical = fs.readFileSync(path.join(root, 'internal/server/version.txt'), 'utf8').trim();
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'web/package.json'), 'utf8'));
  const lock = JSON.parse(fs.readFileSync(path.join(root, 'web/package-lock.json'), 'utf8'));
  const versions = [
    ['internal/server/version.txt', canonical],
    ['web/package.json version', manifest?.version],
    ['web/package-lock.json version', lock?.version],
    ["web/package-lock.json packages[''].version", lock?.packages?.['']?.version],
  ];

  for (const [source, actual] of versions) {
    if (actual !== expected) {
      throw new Error(`${source} 必须为 ${expected}，实际为 ${JSON.stringify(actual)}`);
    }
  }
} catch (error) {
  console.error(`错误：发布版本校验失败：${error.message}`);
  process.exitCode = 1;
}
NODE

printf '%s\n' "$tag"
