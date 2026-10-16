# 维护者发布指南

本文面向发布维护者。用户安装、配置、升级和缓存使用说明见 [README：安装与部署](README.md#安装与部署)；英文用户说明见 [README.en.md](README.en.md)。

发布实现以 [Release 工作流](.github/workflows/release.yml)和 [Nix binary cache 工作流](.github/workflows/nix-cache.yml)为准。完整发布包括正式 GitHub Release，以及同一标签提交的两个架构 Nix 缓存。

## 发布权限与 Cachix 配置

GitHub Release 使用 Actions 自动提供的 `GITHUB_TOKEN`，不需要另外创建个人访问令牌。工作流默认只有 `contents: read`，仅发布作业申请 `contents: write`。

发布前完成 Cachix 配置：

1. 在 [Cachix](https://app.cachix.org) 创建公共缓存，采用托管签名，生成仅限该缓存的写入令牌。项目公共缓存为 [cyl96-file-station](https://app.cachix.org/cache/cyl96-file-station)。
2. 在仓库 **Settings → Secrets and variables → Actions** 设置：
   - Repository variable：`CACHIX_CACHE=cyl96-file-station`。
   - Repository secret：`CACHIX_AUTH_TOKEN`，值为该缓存的写入令牌。
3. 不需要设置 `CACHIX_SIGNING_KEY`。令牌不得写入源码、Nix 配置或聊天；公共缓存下载者不需要写入令牌。

缓存工作流会检查变量和 secret，缺失配置会失败。此工作流在 Release 发布之后执行，缓存失败不会撤回已发布的 Release。

缓存客户端的 URL、公钥和命中条件留在用户 README 中。使用 Cachix 时应保留官方 `https://cache.nixos.org` 以下载通用依赖；`extra-substituters` 不会替换它。更多配置见 [Cachix 文档](https://docs.cachix.org/getting-started.html)和 [cachix-action](https://github.com/cachix/cachix-action)。

## 准备版本并推送标签

1. 确定正式版本，例如 `1.0.0`，将下列四处设为同一个不带 `v` 的版本号：
   - `internal/server/version.txt`。
   - `web/package.json` 的 `version`。
   - `web/package-lock.json` 顶层的 `version`。
   - `web/package-lock.json` 的 `packages[""].version`。
2. 提交版本变更，并将要发布的提交合入 `main`。工作流实际校验标签提交是否已合入仓库默认分支；若调整默认分支，应按新的默认分支操作。
3. 在要发布的提交上创建并推送正式标签。以下示例发布更新后的 `main` HEAD，执行前确认它就是目标发布提交：

   ```sh
   git switch main
   git pull --ff-only
   git tag -a v1.0.0 -m "File Station v1.0.0"
   git push origin v1.0.0
   ```

标签必须是 `vMAJOR.MINOR.PATCH`，各版本段为非负整数且不能有前导零。标签去掉 `v` 后，必须与上述四处版本完全一致。`v1.0.0-rc.1` 等预发布标签不受支持，不会发布。

版本校验由 [`scripts/release-version.sh`](scripts/release-version.sh)实现；发布打包时也会再次调用该校验。

## 自动发布链路

推送 `v*` 标签触发 **Actions → Release**，按顺序执行：

1. **validate**：确认标签提交已合入默认分支，校验正式标签格式及四处版本一致性。
2. **build**：在原生 Linux amd64 和 arm64 runner 上分别构建。Go 版本读取 `go.mod`（当前为 Go 1.26），Node.js 为 24。每个架构都会执行前端依赖安装与构建、`go test ./...`、打包，以及解压后运行二进制 `-h` 并确认 `web/dist/index.html` 存在的检查。
3. **publish**：两个架构均成功后，下载构建产物，生成 `SHA256SUMS`，通过 `gh release create --verify-tag --generate-notes` 发布正式 Release 并自动生成更新说明。
4. **cache**：Release 发布成功后，调用 Nix 缓存工作流，以同一个标签提交构建并上传 `x86_64-linux` 和 `aarch64-linux` 的默认包，同时运行打包后的 `file-station -h`。两个架构均成功才算缓存发布完成。

以 `v1.0.0` 为例，正式 Release 应包含以下附件：

- `file-station-v1.0.0-linux-amd64.tar.gz`
- `file-station-v1.0.0-linux-arm64.tar.gz`
- `SHA256SUMS`，包含上述两个压缩包的 SHA-256 校验值。

每个压缩包有同名顶级目录，包含静态 Go 二进制 `file-station`、`web/dist`、`LICENSE`、`README.md` 和 `README.en.md`，用户运行不依赖 Nix。压缩包由 [`scripts/package-release.sh`](scripts/package-release.sh)生成。GitHub 自动生成的 Source code 附件不是二进制安装包。

容器镜像在各架构构建并加载后、推送 GHCR 前运行 [`scripts/smoke-image.sh`](scripts/smoke-image.sh)。可在有 Docker 的环境中执行 `bash scripts/smoke-image.sh IMAGE [EXPECTED_VERSION]`，将 `IMAGE` 替换为待检查镜像，并按需提供预期版本号。首页引用的 JavaScript 资源必须请求成功、正文非空，且响应 `Content-Type` 为 `text/javascript` 或 `application/javascript`（允许附带 charset 等参数）；不能按正文是否包含 `<html` 判断 SPA 回退，因为合法依赖也可能包含 HTML 模板。

普通分支推送不会触发此发布链路；仅在网页创建 Release 也不会调用后续缓存工作流。

## 失败恢复与标签约束

先在 Actions 中定位失败作业，再按实际发布状态处理：

| 失败位置或状态 | 处理方式 |
| --- | --- |
| 版本或默认分支校验失败 | 修正版本、分支合入状态或发布目标。若需要新的代码提交，使用指向正确提交的新版本标签，不移动已有发布标签。 |
| 构建、测试、打包或构建产物上传失败 | 此时发布作业尚未创建 Release。外部临时故障恢复后可重跑失败作业；需要修改代码时发布新版本。 |
| Release 上传或发布失败 | 先检查 [Releases](https://github.com/CYL96/File-Station/releases) 是否已有草稿或正式 Release。确认并处理未发布草稿后再重跑；不能在不了解现有状态时重复发布。 |
| Release 已正式发布 | 工作流不会覆盖已有 Release。不要移动标签或替换已发布内容；内容需要修复时发布新版本。 |
| 仅后续 Cachix 作业失败 | 修复缓存变量、secret 或构建问题后，在原 Release 工作流运行中选择 **Re-run failed jobs**。不需要重新发布 Release；若修复需要修改发布代码，则发布新版本。 |

已有 Release 时重新运行发布作业会因 `gh release create` 无法重复创建而失败。因此，缓存失败时应重跑失败作业，而不是重新运行整个发布流程。

## 独立构建默认分支缓存

需要为非发布提交构建缓存时，在 **Actions → Nix binary cache → Run workflow** 选择仓库默认分支。手动入口只在默认分支执行构建，不支持选择任意分支或标签来补发缓存。

手动运行同样需要 `CACHIX_CACHE` 和 `CACHIX_AUTH_TOKEN`，并构建、上传两个架构的默认包；它不会创建 GitHub Release，也不替代正式发布流程。非发布提交不保证已有缓存。
