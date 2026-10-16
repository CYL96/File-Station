#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

# 只构建并前台运行；不修改已有服务的程序链接、配置或状态。
printf '正在构建前后端……\n'
package_path="$(nix build "path:$repo_root#default" --no-link --print-out-paths)"

printf '正在当前终端运行，按 Ctrl+C 退出……\n'
exec "$package_path/bin/file-station" "$@"
