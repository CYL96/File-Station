#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

# 构建成功后停止同一配置的旧测试实例并前台运行；不修改程序链接、配置或状态。
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

config_path="$repo_root/test/config.json"
for process in /proc/[0-9]*; do
  [[ -O "$process" && -r "$process/cmdline" ]] || continue
  executable="$(readlink -- "$process/exe" 2>/dev/null)" || continue
  case "$executable" in
    */file-station|*/.file-station-wrapped) ;;
    *) continue ;;
  esac
  args=()
  mapfile -d '' -t args < "$process/cmdline" 2>/dev/null || continue
  process_config=''
  for ((i = 1; i < ${#args[@]}; i++)); do
    if [[ "${args[i]}" == -config && $((i + 1)) -lt ${#args[@]} ]]; then
      process_config="${args[i + 1]}"
      i=$((i + 1))
    elif [[ "${args[i]}" == -config=* ]]; then
      process_config="${args[i]#-config=}"
    fi
  done
  [[ "$process_config" == "$config_path" ]] || continue
  pid="${process##*/}"
  printf '正在停止旧测试实例（PID %s）……\n' "$pid"
  kill -TERM -- "$pid" 2>/dev/null || { [[ ! -d "$process" ]] && continue; exit 1; }
  for ((attempt = 0; attempt < 50; attempt++)); do
    [[ -d "$process" ]] || break
    sleep 0.1
  done
  if [[ -d "$process" ]]; then
    printf '旧测试实例（PID %s）尚未退出，请检查后重试。\n' "$pid" >&2
    exit 1
  fi
done

printf '正在当前终端运行，按 Ctrl+C 退出……\n'
exec "$package_path/bin/file-station" -config "$config_path" "$@"
