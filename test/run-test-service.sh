#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

# 只构建并前台运行；不修改已有服务的程序链接、配置或状态。
# 保留尚未跟踪的源码，但不将被 Git 忽略的私有配置和状态送入 Nix store。
source_dir="$(mktemp -d)"
trap 'rm -rf -- "$source_dir"' EXIT
(
  cd -- "$repo_root"
  git ls-files --cached --others --exclude-standard --deduplicate -z |
    while IFS= read -r -d '' path; do
      if [[ -e "$path" || -L "$path" ]]; then
        cp -pP --parents -- "$path" "$source_dir"
      fi
    done
)

printf '正在构建前后端……\n'
package_path="$(nix build "path:$source_dir#default" --no-link --print-out-paths)"
rm -rf -- "$source_dir"
trap - EXIT

printf '正在当前终端运行，按 Ctrl+C 退出……\n'
exec "$package_path/bin/file-station" -config "$repo_root/test/config.json" "$@"
