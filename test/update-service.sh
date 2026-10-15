#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
service='file-station.service'
out_link="$HOME/.local/state/file-station/current"

# 更新已有用户服务；配置、密码和持久状态保持不变。
systemctl --user cat "$service" > /dev/null

printf '正在构建前后端……\n'
nix build "path:$repo_root#default" --out-link "$out_link"

printf '正在重启用户服务……\n'
systemctl --user restart "$service"
systemctl --user status "$service" --no-pager
