# File Station

**简体中文** | [English](README.en.md)

单管理员 Web 文件管理器，支持 Linux、Docker 和 NixOS。文件留在原目录，SQLite 持久保存设置、共享、任务和会话；宿主机或 Samba 修改文件后，刷新即可查看。

- 多共享、只读共享、搜索排序、列表/网格视图。
- 上传下载、新建、重命名、复制移动、冲突处理和回收站。
- 后台任务、进度、取消和失败重试。
- 图片预览、MP4/WebM 播放、UTF-8 文本编辑、Markdown 源码/预览/可视化编辑。
- 中英双语界面，可设置日期与权限显示格式。

**选择安装方式：** [Docker / Compose](#docker--compose) · [预编译二进制](#预编译二进制) · [源码构建](#源码构建) · [Nix / NixOS](#nix--nixos)

**仅以非 root 用户运行。** 所有文件操作使用服务进程的 Linux 身份，不做多用户映射或提权。首次启动默认没有共享：登录后添加已有目录；Docker 需先挂载数据，再添加容器内路径。

![File Station 中文文件管理界面，展示共享目录导航、文件列表和文件操作工具栏](docs/screenshots/files-list-zh.png)

截图使用演示数据，首次启动仍需自行添加共享。

## 安装与部署

### Docker / Compose

默认使用 GHCR 发布镜像，仅需 Docker Engine 和 Compose 插件，**无需克隆仓库或安装 Go、Node.js**。选择一个部署目录，例如：

```sh
mkdir -p "$HOME/file-station"
cd "$HOME/file-station"
```

在该目录创建 `compose.yaml`：

```yaml
name: file-station

services:
  file-station:
    image: ghcr.io/cyl96/file-station:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      PUID: "${PUID-1000}"
      PGID: "${PGID-1000}"
      UMASK: "${UMASK-0022}"
      PASSWORD: "${PASSWORD-}"
      ConfigPath: "${ConfigPath-/var/lib/file-station}"
    volumes:
      - "./state:${ConfigPath-/var/lib/file-station}"
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETUID
      - SETGID
    security_opt:
      - no-new-privileges:true
```

发布镜像支持 `linux/amd64` 和 `linux/arm64`，Docker 会自动选择宿主架构。`latest` 跟随最新正式版本；如需固定版本，将 `image` 改为 `ghcr.io/cyl96/file-station:X.Y.Z`，标签不带 `v`。例如 `ghcr.io/cyl96/file-station:1.0.0` **仅为格式示例，须替换为实际已发布的版本**；使用前请核对 [Releases](https://github.com/CYL96/File-Station/releases) 对应的镜像标签。

在同一目录创建私有 `.env`（勿提交版本库），内容如下：

```dotenv
PUID=1000
PGID=1000
UMASK='0022'
PASSWORD='replace-with-your-own-long-password'
ConfigPath='/var/lib/file-station'
```

创建后限制文件权限：

```sh
chmod 600 .env
```

将 `PUID` / `PGID` 改为有权访问数据的非 root UID/GID（可用 `id -u` / `id -g` 查看），并替换密码。密码至少 6 字节，建议 12 字节以上；`.env` 中用单引号避免 `$` 插值，直接写在 Compose YAML 中的 `$` 则须写成 `$$`。

```sh
mkdir -p state
docker compose pull
docker compose up -d
```

打开 <http://127.0.0.1:8080>。应用在容器内默认监听 `0.0.0.0:8080`，上例仅发布到宿主回环地址。`./state` 持久挂载到 `ConfigPath`；例如设为 `/config` 时，挂载自动变为 `./state:/config`。**`ConfigPath` 是状态目录，不是 config.json 路径。** 对外访问须显式调整端口映射与防火墙，并阅读[密码与网络](#密码与网络)。

在 `compose.yaml` 现有 `volumes` 中额外挂载共享数据，例如：

```yaml
      - "/srv/documents:/mnt/documents"
      # - "/srv/shared archive:/mnt/archive:ro"
```

重建容器后，在页面添加 `/mnt/documents`，不是宿主路径。未挂载的宿主目录不可见，挂载也不会自动创建共享。

#### 容器身份与权限

- root 入口默认使用 `1000:1000`，只准备状态目录本身，再通过 `su-exec` 降权；不递归修改状态内容或共享。`PUID` / `PGID` 仅接受 `1`–`4294967294` 的十进制整数，无前导零，不能留空。
- 默认无需 `user:`。若指定非 root `--user UID:GID`，未设置的 `PUID` / `PGID` 沿用当前身份；显式不匹配则报错。须提前准备状态目录属主，入口不会提权。上例默认显式传入 `PUID` / `PGID`，使用 `user:` 时须同步调整。
- 保留上例的 `cap_drop: ALL`、仅添加的 `CHOWN` / `SETUID` / `SETGID` 和 `no-new-privileges`。`su-exec` 会清空附加组，不能依赖 `group_add`；按实际 UID、主组或 ACL 授权。rootless Docker 还需考虑宿主 UID 映射。
- **已有状态更换 UID/GID：先停服务、备份，再迁移整个状态目录的属主，保留内容与权限。不要删除数据库，也不要递归 chown 共享数据。**

#### 容器 JSON 模式

需要自定义监听、安全 Cookie 或密码文件时，采用[配置参考](#配置参考)中的 JSON 格式，将 `ip` 设为 `"0.0.0.0"`，所有路径改为容器内路径。让 JSON `stateDir`、`ConfigPath` 和状态挂载目标一致，然后在 Compose 服务中合并：

```yaml
    volumes:
      - './state:${ConfigPath-/var/lib/file-station}'
      - "./config.json:/etc/file-station/config.json:ro"
    command: ["-config", "/etc/file-station/config.json", "-static", "/usr/share/file-station"]
```

合并到已有 `volumes`，不要重复声明该键。JSON 可使用 `password`，或使用 `passwordFile` 并额外只读挂载密码文件；两者只能选一个非空来源。配置及密码须对运行 UID 可读，放在共享之外。覆盖镜像命令时必须保留 `-static`。

JSON 模式下 Go 忽略 `ConfigPath`、`PASSWORD`、`UMASK`，但容器入口仍校验并准备 `ConfigPath`，不会读取 JSON；若与 `stateDir` 不同，实际状态目录的持久挂载和权限须自行准备。仅修改状态目录不需要切换 JSON 模式。

#### 可选：从本地源码构建

仓库中的 `compose.yaml` 仍使用 `build: .` 和 `image: file-station:local`。如需本地构建，获取源码后在仓库根目录按上文创建并设置私有 `.env`（同样执行 `chmod 600 .env`），然后运行：

```sh
mkdir -p state
docker compose up -d --build
```

已有仓库部署如需改用发布镜像，删除 Compose 服务中的 `build`，将 `image` 改为 `ghcr.io/cyl96/file-station:latest` 或实际已发布的固定版本标签；保留原 `.env`、状态目录、挂载、身份及安全设置，按下方[常规升级](#常规升级)的发布镜像步骤切换，**不要重置状态或删除数据库**。

### 预编译二进制

从 [Releases](https://github.com/CYL96/File-Station/releases) 下载匹配 Linux 架构（amd64 或 arm64）的压缩包和 `SHA256SUMS`。**运行不需要 Go、Node.js、npm 或 Nix。** GitHub 自动附带的 Source code 压缩包不是二进制安装包。

先按[本地配置与密码](#本地配置与密码)准备配置、私有密码和可写状态目录，再在下载目录校验、解压并启动。以下以 amd64 的 `v1.0.0` 为例，请替换为实际下载版本；`--ignore-missing` 允许只下载一个架构：

```sh
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf file-station-v1.0.0-linux-amd64.tar.gz
cd file-station-v1.0.0-linux-amd64
./file-station -config "$HOME/.config/file-station/config.json" -static ./web/dist
```

压缩包包含二进制及配套 `web/dist`。以非 root 用户运行，打开 <http://127.0.0.1:8080>。从其他工作目录启动时，将 `-static` 改为解压后 `web/dist` 的绝对路径。

### 源码构建

需要 Linux、Go 1.26、Node.js 24 和 npm；也可先执行 `nix develop path:.`。在仓库根目录构建：

```sh
npm --prefix web ci
npm --prefix web run build
go build -o /tmp/file-station ./cmd/file-station
```

按[本地配置与密码](#本地配置与密码)准备配置后，在仓库根目录用非 root 用户启动：

```sh
/tmp/file-station -config "$HOME/.config/file-station/config.json" -static ./web/dist
```

打开 <http://127.0.0.1:8080>。部署时保留二进制及配套前端；从其他目录启动须使用正确的 `-static` 路径。

### Nix / NixOS

在仓库根目录构建包，按[本地配置与密码](#本地配置与密码)准备配置后运行；包装器已包含前端路径，无需 `-static`：

```sh
nix build path:.#file-station
./result/bin/file-station -config "$HOME/.config/file-station/config.json"
```

`default` 与 `file-station` 包相同，支持 `x86_64-linux`、`aarch64-linux`。

#### NixOS 服务

在已有系统 flake 中合并以下 input，并将 `inputs.file-station.nixosModules.default` 加入主机的 `modules` 列表：

```nix
{
  inputs.file-station.url = "github:CYL96/File-Station";
}
```

在同一 `modules` 列表中加入服务配置（位于 `outputs = inputs@{ ... }: ...` 作用域内，使用外层 `inputs`）：

```nix
{ pkgs, ... }: {
  services.file-station = {
    enable = true;
    package = inputs.file-station.packages.${pkgs.stdenv.hostPlatform.system}.default;
    ip = "127.0.0.1";
    port = 8080;
    passwordFile = "/run/secrets/file-station-password";
    # stateDir = "/var/lib/file-station";
    # supplementaryGroups = [ "documents" ];
    # secureCookies = true; # HTTPS only
  };
}
```

- 模块生成 JSON，无需手写本地配置。部署前准备运行时密码文件，可为 `root:root 0600`；systemd `LoadCredential` 将其交给服务，并保护原始密码源路径。**必须用运行时绝对路径字符串，不得通过 Nix 路径字面量、`writeText` 或其他 store 文件保存密码。**
- 默认创建专用用户/组 `file-station`，无默认共享。可设置 `user` / `group`；自定义用户须已在 `users.users` 声明，组由模块创建。`supplementaryGroups` 只影响服务进程；复用用户会失去专用 UID 的隔离。
- 模块固定 JSON `umask: "0077"` 和 systemd `UMask=0077`。普通数据访问取决于服务账户权限，不另设路径白名单，不递归修改共享目录。模块准备私有状态目录及其内容的属主/组，保留已有状态文件权限。
- 网络磁盘需自行安排挂载顺序，例如 `systemd.services.file-station.unitConfig.RequiresMountsFor = [ "/mnt/documents" ];`。模块不自动开放防火墙。

```sh
sudo nixos-rebuild switch --flake /path/to/system-flake#myhost
systemctl status file-station
journalctl -u file-station
```

#### Cachix 二进制缓存

公共缓存为 [cyl96-file-station](https://app.cachix.org/cache/cyl96-file-station)。NixOS 系统配置加入以下内容，再 rebuild；下载无需令牌，信任公钥即信任维护者提供的构建产物：

```nix
{
  nix.settings = {
    extra-substituters = [ "https://cyl96-file-station.cachix.org" ];
    extra-trusted-public-keys = [ "cyl96-file-station.cachix.org-1:DVc1WCQcvfD3w6trxmPHxDZRo49HxAnojhMoslE4Bio=" ];
  };
}
```

缓存按精确 store 路径匹配。使用上例 `inputs.file-station.packages`，保留项目锁定的 nixpkgs，不设置 `file-station.inputs.nixpkgs.follows`，并锁定到已上传的提交；覆盖源码或构建选项可能无法命中。可用 `nix build --dry-run github:CYL96/File-Station#default` 查看计划；实际日志显示从 Cachix URL 复制路径才证明命中，未命中会本地构建。保留官方 `https://cache.nixos.org` 下载通用依赖；`extra-substituters` 不会替换它。

## 配置

### 本地配置与密码

此步骤共用于预编译二进制、源码构建和直接运行 Nix 包；Docker 默认使用 `.env`，NixOS 模块自行生成配置。在实际运行服务的非 root 账户下准备目录和密码，以下密码命令使用 Bash：

```sh
mkdir -p "$HOME/.config/file-station" "$HOME/.local/state/file-station"
chmod 700 "$HOME/.config/file-station" "$HOME/.local/state/file-station"
read -rs -p 'Administrator password: ' password
printf '\n'
(umask 077; printf '%s' "$password" > "$HOME/.config/file-station/password")
unset password
```

密码至少 6 字节，建议 12 字节以上。创建 `$HOME/.config/file-station/config.json`，将 `/absolute/home` 替换为实际绝对路径（JSON 不展开环境变量）：

```json
{
  "ip": "127.0.0.1",
  "port": 8080,
  "stateDir": "/absolute/home/.local/state/file-station",
  "passwordFile": "/absolute/home/.config/file-station/password",
  "umask": "0022",
  "secureCookies": false,
  "shares": []
}
```

```sh
chmod 600 "$HOME/.config/file-station/config.json" "$HOME/.config/file-station/password"
```

配置和密码应放在所有共享之外。也可将 `passwordFile` 替换为 JSON `password`，但必须恰有一个非空密码来源；不要使用文档示例作为真实密码。

### 配置参考

两种来源**不混用**，启动配置修改后须重启；页面保存共享立即生效。

| 启动方式 | 配置来源与默认行为 |
| --- | --- |
| `-config /path/config.json` | 内置默认值 + 严格 JSON；完全忽略 `ConfigPath`、`PASSWORD`、`UMASK`，不以它们补齐缺项；未知字段、无效 JSON 或多余 JSON 值会导致启动失败 |
| 不传 `-config` 或传空值 | 读取 `PASSWORD`、`UMASK`、`ConfigPath`；监听 `0.0.0.0:8080`，首次零共享，`secureCookies=false` |

`-config` 不从环境变量选择文件。`-static` 独立指定前端目录，默认 `web/dist`，不是 JSON 字段；Docker 和 Nix 已指定包内路径。

| JSON 字段 | 默认值 / 说明 |
| --- | --- |
| `ip` / `port` | `127.0.0.1` / `8080`；IP 地址不带端口或 IPv6 方括号，端口为 `1–65535` |
| `stateDir` | `/var/lib/file-station`；私有、持久、非空绝对目录，须属于运行 UID，权限为 `0700`；普通用户通常须改为可写路径 |
| `password` / `passwordFile` | 必须恰有一个非空；密码至少 6 字节，建议 12 字节以上。文件路径为绝对路径，读取时只去除末尾换行，保留其他空白 |
| `umask` | `"0022"`；3 或 4 位八进制字符串，值不超过 `0777`，不接受数字或空字符串 |
| `secureCookies` | `false`；HTTPS 部署设为 `true`，普通 HTTP 不启用 |
| `shares` | `[]`；仅首次初始化时导入，之后以 `state.sqlite` 为准 |
| `passwordSourceFile` | 空；额外保护原始密码源的绝对路径及可解析真实路径，不读取、不用于认证。NixOS 凭据部署自动设置；每次启动生效，切换密码来源不会自动清除，移除需显式删除或清空 |

| 环境变量 | 默认值 / 说明 |
| --- | --- |
| `PASSWORD` | 无默认值，环境变量模式必填；长度要求同上，JSON 模式忽略 |
| `UMASK` | `0022`，格式同 JSON `umask`，显式空值无效；JSON 模式忽略 |
| `ConfigPath` | `/var/lib/file-station`，大小写精确；状态目录而非 JSON 路径。须为非空绝对目录，不能规范化或解析为 `/`；容器还要求目录本身不是符号链接。Go 的 JSON 模式忽略它，但容器入口仍使用它 |
| `PUID` / `PGID` | 仅容器身份设置，规则见[容器身份与权限](#容器身份与权限)，不属于应用配置 |

普通路径可含空格。上面的 Compose 示例只透传上述五项变量，不自动透传其他宿主变量，不提供短名或旧名别名。

可选 JSON 首次导入：`"shares": [{"id":"documents","name":"文档","path":"/srv/documents","readOnly":false}]`。`name`、`path` 必填，`id` 可省略，`readOnly` 默认 `false`；NixOS 对应 `shares.documents = { name = "文档"; path = "/srv/documents"; readOnly = false; };`，属性名是共享 ID。**即使最初为空，之后修改配置、重启或回滚 generation 也不会重新导入**，请在页面管理共享。

## 使用

### 首次登录与共享

1. 打开部署时配置的地址，使用管理员密码登录。
2. 在「设置 → 共享设置」添加已有、可访问、互不重叠的目录；Docker 填容器内挂载路径。
3. 按需设为只读。共享不能覆盖状态目录或密码路径；目录选择器从 `/` 浏览不等于允许共享 `/`，通常应选择具体数据子目录而非整个 home。

保存立即生效。移除共享不删除文件；文件请求处理中共享变更返回 409，有活动任务时不能修改或移除受影响共享；回收站非空时不能改路径或移除。目录丢失、挂载未就绪或权限不足会标为不可用，不自动删除配置。

### 日常操作与界面设置

- 在列表或网格中浏览、搜索、排序和多选，执行上传下载、新建、重命名、复制、移动及回收站操作；冲突时可跳过、替换文件或保留两份，同名目录不递归覆盖。
- 任务中心显示进度并支持取消。复制、移动和回收站失败任务可重试未完成项，保留旧记录；旧任务缺少重试参数时需重新发起。上传重试仅限当前页面，刷新或退出后须重选文件。重启后未完成任务标为中断，不自动重放。
- 顶部切换中英文，选择保存在当前浏览器。「设置 → 通用设置」设置日期格式及权限列的八进制/符号显示；「高级设置 → 列显示」启用权限列。这些设置不改变文件内容或实际权限。
- 图片可预览；MP4/WebM 使用浏览器原生播放，无服务端转码。MP4 建议 H.264 + AAC，实际支持取决于浏览器和系统，不支持时可下载。
- 文本和 Markdown 可编辑，具体限制见[文件操作与编辑](#文件操作与编辑)。Markdown 可视化编辑期间切换语言会保留正文和未保存状态，但重置撤销历史、光标及未提交插入弹窗；先完成弹窗操作再切换。

## 安全与限制

### 权限与状态备份

- 无 `allowedRoots` 白名单。访问由服务的实际 UID、主组/附加组、Unix 权限、ACL 和挂载选项决定，所有父目录须可遍历。只读共享只是额外限制；需系统级保护时使用只读挂载或 ACL。应用检查不能替代系统隔离，挂载点和硬链接仍可能暴露数据。
- **保留整个状态目录及共享 ID**，不要删除 `state.sqlite` 重置设置。状态目录不能临时化、与共享重叠或在部署时被替换。
- 回收站内容位于共享下 `.webui-trash`，占用该文件系统空间，元数据在状态库。停服务及其他写入后，一致备份共享文件、隐藏回收站和整个状态目录，并私密备份启动配置及密码。**回收站不是备份，NixOS 回滚也不会撤销文件或数据库变更。**

### 文件操作与编辑

- 共享根路径会解析符号链接；共享内部不跟随符号链接，不能进入、下载、预览或复制链接，仅能移动、重命名或回收链接本身。只处理普通文件和目录，不能删除共享根或通过普通接口操作 `.webui-*` 内部路径。
- 新文件权限为 `0666 & ~umask`，新目录为 `0777 & ~umask`；默认 `0022` 对应 `0644/0755`，NixOS `0077` 对应 `0600/0700`，仍受系统 ACL 等规则约束。文本保存、同文件系统移动和回收站恢复保留原权限；复制不保留所有者、ACL、扩展属性、硬链接关系或原始时间。
- 上传和复制先写隐藏暂存文件；取消不回滚已完成项，中断可能残留暂存文件，确认无任务后再清理。同文件系统移动为原子重命名，跨文件系统先复制后删除，**不是整体事务**，失败可能两端都保留。
- 文本编辑限已有、不含 NUL、≤1 MiB 的 UTF-8 普通文件；只读共享、回收站、无写权限或多硬链接文件不能保存。保存须保留权限、属主/组、ACL 和扩展属性，否则拒绝；原子替换会改变 inode 和时间。外部修改报告冲突并保留草稿，但**不是文件系统级 CAS**；统一 CRLF 保留，其他换行统一为 LF。
- Markdown 预览不执行原始 HTML、不自动加载外部图片。可视化编辑会重新序列化 Markdown；需要逐字保留时用源码编辑。

### 密码与网络

- 密码文件和含密码配置设为 `0600`，置于共享之外，不提交仓库、镜像层或 Nix store。环境变量及 Compose 明文可被 `docker inspect` 或有权限的宿主管理员读取；需要减少环境变量暴露时使用只读 JSON / 密码文件。
- 会话有效期 12 小时，Cookie 使用 HttpOnly、SameSite=Strict，并有 CSRF 防护。HTTPS 代理下启用 `secureCookies`（NixOS 同名选项；Docker 需 JSON 模式）；普通 HTTP 启用后浏览器不会发送会话 Cookie。
- 反向代理保留 Host，页面与 API 同源；关闭 `/api/events` 的 SSE 缓冲并延长连接超时，按需设置上传大小限制。仅向可信网络开放必要端口，优先使用 HTTPS；NixOS 不自动开放防火墙。
- 「高级设置 → 关于 File Station → 检查更新」由服务端访问 GitHub，仅检查正式 Release 并提供发布页链接，**不自动下载安装**。

## 升级与旧版迁移

### 常规升级

升级前查看目标版本说明，准备好新版本，再停服务并一致备份状态、共享文件及隐藏回收站，同时保留配置和密码。保留原状态路径、共享 ID、运行 UID/GID 和必要启动参数，禁止新旧实例同时访问同一状态库。

| 安装方式 | 更新方式 |
| --- | --- |
| Docker / Compose（发布镜像） | 将 `image` 设为目标发布标签，运行 `docker compose pull`；`docker compose stop` 后完成上述一致备份，再运行 `docker compose up -d`。保留 `.env`、`./state`、挂载和运行 UID/GID |
| Docker / Compose（本地源码构建） | 更新本地仓库到目标版本，保留 `.env`、`./state`、挂载和运行 UID/GID；停服务并完成上述一致备份后，运行 `docker compose up -d --build` |
| 预编译二进制 | 下载目标版本及校验和，校验并解压到新目录；停止旧进程，将启动命令或服务改为新二进制和配套 `web/dist`，使用原配置启动 |
| 源码构建 | 更新源码后重新构建前后端；停止旧进程，部署配套产物并使用原配置启动 |
| Nix 包 | 更新源码或 flake 锁定版本，重新 `nix build`，将启动路径切换到新包并重启；已有用户服务见下节 |
| NixOS 服务 | 更新系统 flake 中 `file-station` 的锁定版本，执行 `nixos-rebuild switch` |

升级后重新访问页面，确认共享、设置和文件正常。切回旧程序或 NixOS generation 不会还原文件与状态数据库；需要恢复时使用一致备份。

### 已有用户服务

仅当已有 `file-station.service` 用户服务，且 `ExecStart` 指向 `$HOME/.local/state/file-station/current/bin/file-station` 时，可从更新后的仓库运行：

```sh
./test/update-service.sh
```

脚本构建 Nix 包、更新 `current` 链接并重启用户服务；不安装服务，不修改配置、密码或状态，也不更新 NixOS 系统服务。构建失败或找不到该用户 unit 时不会重启。服务使用其他启动路径时，按实际部署更新路径并重启。旧名称服务须先手动迁移，脚本不会迁移或重启它。

### 旧版迁移

旧二进制/包名 `nixos-file-station`、`services.nixos-file-station`、旧 unit 和 `FILE_STATION_*` 环境变量已移除，无兼容别名；改用 `file-station` / `services.file-station` 和上文配置。删除旧 `allowedRoots`，改用系统权限授权；旧 Compose secret 须移除或改为显式 JSON 模式。仓库地址为 `CYL96/File-Station`。

1. **先取得或构建成功的新版本，再停旧服务并备份**完整状态、共享及回收站、配置、密码和 unit/drop-in；备份放在状态及所有共享之外。禁止新旧服务同时访问同一状态库。
2. 显式保留原状态路径（旧默认 `/var/lib/nixos-file-station`，新默认 `/var/lib/file-station`）：JSON 用 `stateDir`，Docker 环境变量模式用 `ConfigPath`，容器 JSON 模式同时匹配配置与挂载，NixOS 用 `services.file-station.stateDir`。也可停机完整迁移并正确调整属主，**绝不能删库重建**。
3. 保留原密码与必要启动设置。旧环境密码须迁入 JSON `password` 或 `passwordFile`；systemd 凭据和 `passwordSourceFile` 保护按需保留。自定义监听等配置改用 JSON，前端目录只用 `-static`。新默认 `0022` 比旧权限宽；需旧 `0640/0750` 时设置 `0027`（Docker 用 `UMASK`），NixOS 仍为 `0077`。
4. NixOS 更新模块名和选项后 rebuild。旧用户服务先用 `systemctl --user cat nixos-file-station.service` 检查原配置；步骤 1 停机前可用 `mkdir -p "$HOME/.local/state/file-station" && nix build path:.#default --out-link "$HOME/.local/state/file-station/current"` 准备新包。备份后创建 `~/.config/systemd/user/file-station.service`（必要时先创建父目录），保留凭据、挂载依赖和自定义项。将 `ExecStart` 改为 `%h/.local/state/file-station/current/bin/file-station -config "/absolute/path/to/existing-config.json"`，替换为真实的原配置路径并保留引号；Nix 包无需另传 `-static`。已有新 unit/drop-in 时先备份、合并，不直接覆盖。然后依次执行：

   ```sh
   systemctl --user stop nixos-file-station.service
   systemctl --user disable nixos-file-station.service
   systemctl --user daemon-reload
   systemctl --user enable --now file-station.service
   systemctl --user status file-station.service --no-pager
   ```

5. 会话 Cookie 已更名，需重新登录。确认原共享、设置和文件仍在，迁移监控、日志命令及 unit 覆盖；用户服务日志用 `journalctl --user -u file-station.service`。后续符合上节路径约定的用户服务可使用更新脚本。

维护者发布说明：[RELEASING.md](https://github.com/CYL96/File-Station/blob/main/RELEASING.md)。
