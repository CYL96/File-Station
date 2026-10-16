#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 || -z ${3:-} ]]; then
  printf '错误：用法：bash scripts/package-release.sh TAG ARCH OUTDIR\n' >&2
  exit 1
fi

tag=$1
arch=$2
outdir=$3
case "$arch" in
  amd64|arm64) ;;
  *)
    printf '错误：不支持的 Linux 架构：%s（仅支持 amd64 或 arm64）。\n' "$arch" >&2
    exit 1
    ;;
esac

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tag=$(bash "$repo_root/scripts/release-version.sh" "$tag")
if [[ ! -f "$repo_root/web/dist/index.html" ]]; then
  printf '错误：缺少 web/dist/index.html，请先在 web 目录执行 npm ci 和 npm run build。\n' >&2
  exit 1
fi

mkdir -p -- "$outdir"
outdir=$(realpath -- "$outdir")
workdir=$(mktemp -d "$outdir/.file-station-release.XXXXXX")
trap 'rm -rf -- "$workdir"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

name="file-station-$tag-linux-$arch"
package_dir="$workdir/$name"
mkdir -p -- "$package_dir/web"

cd -- "$repo_root"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
  -trimpath -ldflags='-s -w' \
  -o "$package_dir/file-station" ./cmd/file-station
cp -- LICENSE README.md README.en.md "$package_dir/"
cp -R -- web/dist "$package_dir/web/"

tar -czf "$workdir/$name.tar.gz" -C "$workdir" -- "$name"
mv -- "$workdir/$name.tar.gz" "$outdir/$name.tar.gz"
printf '%s\n' "$outdir/$name.tar.gz"
