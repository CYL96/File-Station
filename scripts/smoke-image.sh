#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf '错误：%s\n' "$1" >&2
  exit 1
}

if [[ $# -lt 1 || $# -gt 2 || -z $1 ]]; then
  fail '用法：bash scripts/smoke-image.sh IMAGE [EXPECTED_VERSION]'
fi

image=$1
expected_version=${2-}
for command in docker curl jq mktemp cat sleep rm rmdir; do
  command -v "$command" >/dev/null 2>&1 || fail "容器冒烟检查需要安装 $command。"
done

container=
volume=
tmp_dir=
container_created=0
volume_created=0
cleanup() {
  local status=$?
  trap - EXIT
  if [[ $status -ne 0 && $container_created -eq 1 ]]; then
    printf '\n容器日志：\n' >&2
    docker logs "$container" >&2 || true
  fi
  if [[ $container_created -eq 1 ]]; then
    if ! docker rm --force "$container" >/dev/null; then
      printf '错误：无法清理冒烟检查容器：%s\n' "$container" >&2
      status=1
    fi
  fi
  if [[ $volume_created -eq 1 ]]; then
    if ! docker volume rm "$volume" >/dev/null; then
      printf '错误：无法清理冒烟检查卷：%s\n' "$volume" >&2
      status=1
    fi
  fi
  if [[ -n $tmp_dir ]]; then
    if ! { rm -f -- "$tmp_dir"/* && rmdir -- "$tmp_dir"; }; then
      printf '错误：无法清理冒烟检查临时目录：%s\n' "$tmp_dir" >&2
      status=1
    fi
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/file-station-smoke.XXXXXXXXXX")
container=${tmp_dir##*/}
volume=$container
docker volume create "$volume" >/dev/null || fail '无法创建冒烟检查卷。'
volume_created=1
# Keep the default entrypoint: it must prepare the volume and drop privileges.
# Mark the named container before starting so a failed start is also cleaned up.
container_created=1
docker run --detach \
  --name "$container" \
  --cap-drop ALL \
  --cap-add CHOWN \
  --cap-add SETUID \
  --cap-add SETGID \
  --security-opt no-new-privileges:true \
  --publish 127.0.0.1::8080 \
  --mount "type=volume,source=$volume,target=/var/lib/file-station" \
  --env PUID=12345 \
  --env PGID=12345 \
  --env PASSWORD=file-station-smoke-only \
  "$image" >/dev/null || fail '无法启动待检查镜像。'

address=$(docker port "$container" 8080/tcp) || fail '无法读取容器的随机回环端口。'
[[ $address =~ ^127\.0\.0\.1:[0-9]+$ ]] || fail "容器未绑定唯一的随机回环端口：$address"
base_url="http://$address"

# Save the final curl error and response rather than hiding readiness failures.
deadline=$((SECONDS + 60))
ready=0
while (( SECONDS < deadline )); do
  if curl --silent --show-error --fail-with-body --noproxy '*' \
    --connect-timeout 1 --max-time 2 \
    --output "$tmp_dir/index.html" "$base_url/" 2>"$tmp_dir/readiness-error"; then
    ready=1
    break
  fi
  running=$(docker inspect --format '{{.State.Running}}' "$container") || fail '无法读取容器运行状态。'
  if [[ $running != true ]]; then
    cat "$tmp_dir/readiness-error" >&2
    fail '容器在 HTTP 服务就绪前退出。'
  fi
  sleep 1
done
if [[ $ready -ne 1 ]]; then
  cat "$tmp_dir/readiness-error" >&2
  [[ ! -f $tmp_dir/index.html ]] || cat "$tmp_dir/index.html" >&2
  fail '等待容器 HTTP 服务就绪超时（60 秒）。'
fi

request() {
  local output=$1
  local endpoint=$2
  shift 2
  if ! curl --silent --show-error --fail-with-body --noproxy '*' \
    --connect-timeout 2 --max-time 10 \
    --output "$output" "$@" "$base_url$endpoint"; then
    [[ ! -f $output ]] || cat "$output" >&2
    fail "HTTP 检查失败：$endpoint"
  fi
}

html=$(<"$tmp_dir/index.html")
[[ $html == *'<title>File Station</title>'* && $html == *'<div id="root"></div>'* ]] || fail '首页未返回 File Station 的真实静态页面。'
asset_pattern='src="(/assets/[^"[:space:]]+\.js)"'
[[ $html =~ $asset_pattern ]] || fail '首页缺少构建后的 JavaScript 静态资源。'
js_content_type=$(request "$tmp_dir/app.js" "${BASH_REMATCH[1]}" --write-out '%{content_type}')
[[ -s $tmp_dir/app.js ]] || fail '首页 JavaScript 静态资源为空。'
case "${js_content_type%%;*}" in
  text/javascript|application/javascript) ;;
  *) fail "JavaScript 静态资源的 Content-Type 不正确：${js_content_type:-（缺失）}" ;;
esac

process_status=$(docker exec "$container" cat /proc/1/status) || fail '无法读取实际应用进程的身份。'
uid_seen=0
gid_seen=0
name_seen=0
while read -r key real effective saved filesystem _; do
  case "$key" in
    Name:)
      [[ $real == file-station ]] || fail "容器主进程不是 File Station：$real"
      name_seen=1
      ;;
    Uid:|Gid:)
      [[ $real == 12345 && $effective == 12345 && $saved == 12345 && $filesystem == 12345 ]] || fail "应用进程的 $key 未全部降权为 12345。"
      if [[ $key == Uid: ]]; then uid_seen=1; else gid_seen=1; fi
      ;;
  esac
done <<< "$process_status"
[[ $uid_seen -eq 1 && $gid_seen -eq 1 && $name_seen -eq 1 ]] || fail '应用进程身份信息不完整。'

state_metadata=$(docker exec --user 12345:12345 "$container" stat -c '%a %u %g' /var/lib/file-station) || fail '无法读取状态目录权限。'
[[ $state_metadata == '700 12345 12345' ]] || fail "状态目录权限或属主错误：$state_metadata"
database_metadata=$(docker exec --user 12345:12345 "$container" stat -c '%a %u %g' /var/lib/file-station/state.sqlite) || fail '持久卷中不存在 SQLite 状态文件。'
[[ $database_metadata == '600 12345 12345' ]] || fail "SQLite 状态文件权限或属主错误：$database_metadata"
docker exec --user 12345:12345 "$container" test -s /var/lib/file-station/state.sqlite || fail 'SQLite 状态文件为空。'

request "$tmp_dir/login.json" /api/login \
  --header 'Content-Type: application/json' \
  --data '{"password":"file-station-smoke-only"}' \
  --cookie-jar "$tmp_dir/cookies"
jq -e '.username == "admin" and (.csrf | type == "string" and length > 0)' "$tmp_dir/login.json" >/dev/null || fail '登录响应未包含有效的管理员会话。'
request "$tmp_dir/session.json" /api/session --cookie "$tmp_dir/cookies"
jq -e --slurpfile login "$tmp_dir/login.json" '. == $login[0]' "$tmp_dir/session.json" >/dev/null || fail '登录 Cookie 未能恢复同一管理员会话。'
request "$tmp_dir/shares.json" /api/shares --cookie "$tmp_dir/cookies"
jq -e '. == []' "$tmp_dir/shares.json" >/dev/null || fail '全新状态卷的初始共享列表不是空数组。'

if [[ -n $expected_version ]]; then
  request "$tmp_dir/about.json" /api/settings/about --cookie "$tmp_dir/cookies"
  jq -e --arg version "$expected_version" '.version == $version' "$tmp_dir/about.json" >/dev/null || fail "镜像内的软件版本不符合预期 $expected_version。"
fi

printf '镜像冒烟检查通过：%s（应用身份 12345:12345）\n' "$image"
