# File Station

单管理员 Web 文件管理器，支持 Linux、Docker 和 NixOS。文件留在原目录，SQLite 保存设置、共享、任务和会话；宿主机或 Samba 修改文件后，刷新即可查看。

- 多共享、只读共享、搜索排序、列表/网格视图。
- 上传下载、新建、重命名、复制移动、冲突处理、回收站。
- 后台任务、进度、取消和失败重试。
- 图片预览、UTF-8 文本编辑、Markdown 源码/预览/可视化编辑。

**仅以非 root 用户运行。首次启动没有共享：登录后在「共享设置」添加已有目录；Docker 需先挂载，再添加容器内路径。** 所有操作使用服务进程的 Linux 身份，不做多用户映射或提权。

部署方式：[Docker / Compose](#docker--compose) · [本地运行](#本地运行) · [Nix / NixOS](#nix--nixos)

## Docker / Compose

在仓库根目录创建私有 `.env`（权限 `0600`，勿提交版本库）：

```dotenv
PUID=1000
PGID=1000
UMASK='0022'
PASSWORD='替换为你自己的长密码'
ConfigPath='/var/lib/file-station'
```

将 `PUID` / `PGID` 改为有权访问数据的非 root UID/GID（可用 `id -u` / `id -g` 查看）。密码至少 6 字节，建议 12 字节以上；`.env` 中用单引号避免 `$` 插值，直接写在 Compose YAML 中的 `$` 则须写成 `$$`。

```sh
mkdir -p state
docker compose up -d --build
```

打开 <http://127.0.0.1:8080>。默认只发布到宿主回环地址，`./state` 持久挂载到 `ConfigPath`；例如设为 `/config` 时，挂载自动变为 `./state:/config`。**`ConfigPath` 是状态目录，不是 config.json 路径。**

共享数据需在 `compose.yaml` 的 `volumes` 中额外挂载，例如：

```yaml
      - /srv/documents:/mnt/documents
      # - /srv/archive:/mnt/archive:ro
```

重建容器后，在页面添加 `/mnt/documents`，不是宿主路径。未挂载的宿主目录不可见，挂载也不会自动创建共享。

### 容器身份与权限

- root 入口默认使用 `1000:1000`，只准备状态目录本身，再通过 `su-exec` 降权；不递归修改状态内容或共享。`PUID` / `PGID` 仅接受 `1`–`4294967294` 的十进制整数，无前导零，不能留空。
- 默认无需 `user:`。若指定非 root `--user UID:GID`，未设置的 `PUID` / `PGID` 沿用当前身份；显式不匹配则报错。须提前准备状态目录属主，入口不会提权。
- 保留仓库 Compose 的 `cap_drop: ALL`、`CHOWN` / `SETUID` / `SETGID` 和 `no-new-privileges`。`su-exec` 会清空附加组，不能依赖 `group_add`；按实际 UID、主组或 ACL 授权。rootless Docker 还需考虑宿主 UID 映射。
- **已有状态更换 UID/GID：先停服务、备份，再迁移整个状态目录的属主，保留内容与权限。不要删除数据库，也不要递归 chown 共享数据。**

### 可选：容器使用 JSON

需要自定义监听、安全 Cookie 或密码文件时，使用下文 JSON 格式，并将 `ip` 设为 `"0.0.0.0"`，路径改为容器内路径。让 JSON `stateDir`、`ConfigPath` 和状态挂载目标一致，然后在 Compose 服务中增加：

```yaml
    volumes:
      - './state:${ConfigPath-/var/lib/file-station}'
      - ./config.json:/etc/file-station/config.json:ro
    command: ["-config", "/etc/file-station/config.json", "-static", "/usr/share/file-station"]
```

合并到已有 `volumes`，不要重复声明该键。JSON 及密码文件须对运行 UID 可读，放在共享之外；覆盖镜像命令时必须保留 `-static`。Go 文件模式忽略业务环境变量，但容器入口仍校验并准备 `ConfigPath`，不会读取 JSON；若与 `stateDir` 不同，实际状态目录的持久挂载和权限须自行准备。

## 本地运行

需要 Linux、Go 1.26、Node.js 24 和 npm；也可先执行 `nix develop path:.`。在仓库根目录用普通用户运行，以下密码命令使用 Bash：

```sh
npm --prefix web ci
npm --prefix web run build
go build -o /tmp/file-station ./cmd/file-station

mkdir -p "$HOME/.config/file-station" "$HOME/.local/state/file-station"
chmod 700 "$HOME/.config/file-station" "$HOME/.local/state/file-station"
read -rs -p 'Administrator password: ' password
printf '\n'
(umask 077; printf '%s' "$password" > "$HOME/.config/file-station/password")
unset password
```

创建 `$HOME/.config/file-station/config.json`，将 `/absolute/home` 替换为实际绝对路径（JSON 不展开环境变量）：

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
/tmp/file-station -config "$HOME/.config/file-station/config.json" -static ./web/dist
```

打开 <http://127.0.0.1:8080>。配置和密码应放在所有共享之外。

## Nix / NixOS

直接构建运行（配置同上；包装器已包含前端路径）：

```sh
nix build path:.#file-station
./result/bin/file-station -config "$HOME/.config/file-station/config.json"
```

`default` 与 `file-station` 包相同，支持 `x86_64-linux`、`aarch64-linux`。在已有系统 flake 中合并以下 input，并将 `inputs.file-station.nixosModules.default` 加入主机的 `modules` 列表：

```nix
{
  inputs.file-station.url = "github:CYL96/File-Station";
  inputs.file-station.inputs.nixpkgs.follows = "nixpkgs";
}
```

系统配置示例：

```nix
services.file-station = {
  enable = true;
  ip = "127.0.0.1";
  port = 8080;
  passwordFile = "/run/secrets/file-station-password";
  # stateDir = "/var/lib/file-station";
  # supplementaryGroups = [ "documents" ];
};
```

- 部署前准备运行时密码文件，可为 `root:root 0600`；systemd `LoadCredential` 将其交给服务。**必须用运行时绝对路径字符串，不得通过 Nix 路径字面量、`writeText` 或其他 store 文件保存密码。**
- 默认创建专用用户/组 `file-station`，无默认共享。可设置 `user` / `group`；自定义用户须已在 `users.users` 声明，组由模块创建。`supplementaryGroups` 只影响服务进程；复用用户会失去专用 UID 的隔离。
- 模块使用 JSON 模式，固定 `umask: "0077"` 和 systemd `UMask=0077`。普通数据访问取决于服务账户权限，不另设路径白名单；模块不递归修改共享目录。
- 网络磁盘需自行安排挂载顺序，例如 `systemd.services.file-station.unitConfig.RequiresMountsFor = [ "/mnt/documents" ];`。模块不自动开放防火墙。

部署与排错：

```sh
sudo nixos-rebuild switch --flake /path/to/system-flake#myhost
systemctl status file-station
journalctl -u file-station
```

若使用的是用户服务，且 `ExecStart` 指向 `$HOME/.local/state/file-station/current/bin/file-station`，可用 `./test/update-service.sh` 构建 Nix 包、更新链接并重启 `file-station.service`。脚本不安装服务、不修改配置或状态，也不更新 NixOS 系统服务；构建失败或找不到该用户 unit 时不会重启。

## 配置参考

两种来源**不混用**，启动配置修改后须重启；页面保存共享立即生效。

| 启动方式 | 配置来源与默认行为 |
| --- | --- |
| `-config /path/config.json` | 内置默认值 + 严格 JSON；完全忽略 `ConfigPath`、`PASSWORD`、`UMASK`，不以它们补齐缺项；未知字段、无效 JSON 或多余 JSON 值会导致启动失败 |
| 不传 `-config` 或传空值 | 读取 `PASSWORD`、`UMASK`、`ConfigPath`；监听 `0.0.0.0:8080`，首次零共享，`secureCookies=false` |

`-config` 不从环境变量选择文件。`-static` 独立指定前端目录，默认 `web/dist`；Docker 和 Nix 已指定包内路径。

| JSON 字段 | 默认值 / 说明 |
| --- | --- |
| `ip` / `port` | `127.0.0.1` / `8080`；IP 地址不带端口或 IPv6 方括号，端口为 `1–65535` |
| `stateDir` | `/var/lib/file-station`；私有、持久、非空绝对目录，须属于运行 UID，权限为 `0700` |
| `password` / `passwordFile` | 必须恰有一个非空；密码至少 6 字节，建议 12 字节以上。文件路径为绝对路径，读取时只去除末尾换行 |
| `umask` | `"0022"`；3 或 4 位八进制字符串，值不超过 `0777`，不接受数字或空字符串 |
| `secureCookies` | `false`；HTTPS 部署设为 `true`，普通 HTTP 不启用 |
| `shares` | `[]`；仅首次初始化时导入，之后以 `state.sqlite` 为准 |
| `passwordSourceFile` | 空；额外保护原始密码源路径，不读取、不用于认证。NixOS 凭据部署自动设置；每次启动生效，切换密码来源不会自动清除，移除需显式删除或清空 |

环境变量模式的 `PASSWORD` 无默认值；`UMASK` 默认 `0022`、格式同上；`ConfigPath` 默认 `/var/lib/file-station`，大小写精确，须为非空绝对目录，不能规范化或解析为 `/`。容器还要求该目录本身不是符号链接。普通路径可含空格。Docker 只透传这三项业务变量及 `PUID` / `PGID`，不提供其他短名或旧名别名。

可选 JSON 首次导入：`"shares": [{"id":"documents","name":"文档","path":"/srv/documents","readOnly":false}]`。`name`、`path` 必填，`id` 可省略，`readOnly` 默认 `false`；NixOS 对应 `shares.documents = { name = "文档"; path = "/srv/documents"; };`。即使最初为空，之后修改配置、重启或回滚 generation 也不会重新导入。

## 使用与安全边界

### 共享、权限与持久化

- 共享须是已存在、可访问、互不重叠的目录，不能覆盖状态或密码。目录选择器从 `/` 浏览不等于允许共享 `/`；共享整个 home 也可能因包含私有数据而被拒绝。
- 无 `allowedRoots` 白名单。访问由实际 UID、主组/附加组、Unix 权限、ACL 和挂载选项决定，所有父目录须可遍历。只读共享只是额外限制；需系统级保护时使用只读挂载或 ACL。应用检查不能替代系统隔离，挂载点和硬链接仍可能暴露数据。
- 移除共享不删除文件。文件请求处理中共享变更返回 409；受影响共享有活动任务时不能修改或移除；回收站非空时不能改路径或移除。目录丢失或权限不足会标为不可用，不自动删除配置。
- **保留整个状态目录及共享 ID**，不要删除 `state.sqlite` 重置设置。回收站内容位于共享下 `.webui-trash`，元数据在状态库；停服务及其他写入后，一致备份共享、隐藏回收站和状态目录。**回收站不是备份，NixOS 回滚也不会撤销文件或数据库变更。**

### 文件操作与编辑

- 共享根路径会解析符号链接；共享内部不跟随符号链接，仅能移动、重命名或回收链接本身。只处理普通文件和目录，不能删除共享根或通过普通接口操作 `.webui-*` 内部路径。
- 新文件权限为 `0666 & ~umask`，新目录为 `0777 & ~umask`；默认 `0022` 对应 `0644/0755`，NixOS `0077` 对应 `0600/0700`。文本保存、同文件系统移动和回收站恢复保留原权限；复制不保留所有者、ACL、扩展属性、硬链接关系或原始时间，同名目录不递归覆盖。
- 上传和复制先写隐藏暂存文件；取消不回滚已完成项，中断可能残留暂存文件。同文件系统移动为原子重命名，跨文件系统先复制后删除，不是整体事务，失败可能两端都保留。
- 文本编辑限已有、不含 NUL、≤1 MiB 的 UTF-8 普通文件；只读、无写权限或多硬链接文件不能保存。保存须保留权限、属主/组、ACL 和扩展属性，否则拒绝；原子替换会改变 inode 和时间。外部修改报告冲突并保留草稿，但不是文件系统级 CAS；统一 CRLF 保留，其他换行统一为 LF。
- Markdown 预览不执行原始 HTML、不自动加载外部图片。可视化编辑会重新序列化 Markdown；需要逐字保留时用源码编辑。
- 重启后未完成任务标为中断，不自动重放。复制、移动和回收站任务可重试未完成项并保留旧记录；旧任务缺少重试参数时需重新发起。上传重试仅限当前页面，刷新或退出后须重选文件。

### 密码与网络

- 密码文件和含密码配置设为 `0600`，置于共享之外，不提交仓库、镜像层或 Nix store。环境变量及 Compose 明文可被 `docker inspect` 或有权限的宿主管理员读取；需要减少环境变量暴露时使用只读 JSON / 密码文件。
- 会话有效期 12 小时，Cookie 使用 HttpOnly、SameSite=Strict，并有 CSRF 防护。HTTPS 代理下启用 `secureCookies`（NixOS 同名选项；Docker 需 JSON 模式）。
- 代理保留 Host，页面与 API 同源；关闭 `/api/events` 的 SSE 缓冲并延长连接超时，按需设置上传大小限制。仅向可信网络开放必要端口，优先使用 HTTPS。
- 「高级设置 → 关于 File Station → 检查更新」由服务端访问 GitHub，仅检查正式 Release，不自动下载安装。

## 旧版迁移

旧二进制/包名 `nixos-file-station`、`services.nixos-file-station`、旧 unit 和 `FILE_STATION_*` 环境变量已移除，无兼容别名；改用 `file-station` / `services.file-station` 和上文配置。删除旧 `allowedRoots`，改用系统权限授权；旧 Compose secret 须移除或改为显式 JSON 模式。

1. **先构建成功，再停旧服务并备份**完整状态、配置、密码和 unit/drop-in；备份放在状态及所有共享之外。禁止新旧服务同时访问同一状态库。
2. 显式保留原状态路径（旧默认 `/var/lib/nixos-file-station`，新默认 `/var/lib/file-station`）：JSON 用 `stateDir`，Docker 环境变量模式用 `ConfigPath`，容器 JSON 模式同时匹配配置与挂载。也可停机完整迁移，正确调整属主，绝不能删库重建。
3. 保留原密码与必要启动设置。旧环境密码须迁入 JSON `password` 或 `passwordFile`；systemd 凭据和 `passwordSourceFile` 保护按需保留。新默认 `0022` 比旧权限宽；需旧 `0640/0750` 时设置 `0027`，NixOS 仍为 `0077`。
4. NixOS 更新模块名后 rebuild。旧用户服务则用 `systemctl --user cat nixos-file-station.service` 检查原配置，创建 `file-station.service`（保留凭据、挂载依赖和自定义项），将 `ExecStart` 改为新包路径并显式传入原 `-config`；禁用旧 unit，再 `daemon-reload` 和 `enable --now file-station.service`。
5. 重新登录，确认原共享、设置和文件仍在；迁移监控、日志命令及 unit 覆盖。仓库现名为 `CYL96/File-Station`。后续用户服务更新可使用上文脚本，脚本不会迁移或重启旧名称服务。
