#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
service='file-station.service'
out_link="$HOME/.local/state/file-station/current"

# 更新已有用户服务；配置、密码和持久状态保持不变。
if ! systemctl --user cat "$service" > /dev/null 2>&1; then
  printf '%s\n' \
    '更新需要已配置的 file-station.service 用户服务；当前无法读取该 unit，未构建或重启。' \
    '若仍使用 nixos-file-station.service，请先按 README「从旧名称迁移」手动迁移服务名和二进制路径，并保留原 stateDir 与密码来源。' \
    '本脚本不会安装服务，也不会更新或重启旧服务。' >&2
  exit 1
fi

printf '正在构建前后端……\n'
nix build "path:$repo_root#default" --out-link "$out_link"

printf '正在重启用户服务……\n'
systemctl --user restart "$service"
systemctl --user status "$service" --no-pager
