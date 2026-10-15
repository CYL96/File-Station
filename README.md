# NixOS File Station

面向 NixOS 的单管理员 Web 文件管理器，使用 React + TypeScript 前端、Go HTTP 服务和 SQLite 持久状态。文件内容直接保留在共享目录中，不导入数据库；宿主机或 Samba 修改文件后，刷新即可查看实际目录状态。

应用以非 root 服务账户操作文件，不提供多用户 Linux 身份映射、root 权限或 NixOS 系统管理能力，也不会修改系统配置或执行 `nixos-rebuild`。

## 主要功能

- 管理员密码登录、12 小时会话、退出和 CSRF 防护。
- 多共享目录浏览与管理，支持只读共享、搜索当前目录名称、排序、多选、列表/网格视图；高级设置可保存隐藏项目及列显示设置。
- 新建文件夹、重命名、复制、移动、上传、下载、计算大小；同名冲突可跳过、替换文件或保留两份。
- 回收站、恢复与确认后永久删除；后台任务、上传进度及取消操作。
- PNG/JPEG/GIF/WebP 图片预览，以及不超过 1 MiB 的 UTF-8 文本预览与编辑。
- Markdown 渲染、源码编辑、源码与预览对照、可视化编辑。可视化修改会重新序列化 Markdown，可能规范化空格、列表、表格或不支持的语法；需要逐字保留时使用源码编辑。

## 本地安装与运行

需要 Go 1.26、Node.js 24 和 npm；也可以运行 `nix develop path:.` 进入开发环境。以下命令在仓库根目录的 Bash 中执行，使用普通用户，不要用 root 启动程序。

```sh
npm --prefix web ci
npm --prefix web run build
go build -o /tmp/nixos-file-station ./cmd/nixos-file-station

mkdir -p "$HOME/file-station-data/documents" "$HOME/file-station-data/media" \
  "$HOME/.local/state/nixos-file-station" "$HOME/.config/nixos-file-station"
chmod 700 "$HOME/.local/state/nixos-file-station" "$HOME/.config/nixos-file-station"

# 密码至少 6 字节；长期部署建议至少 12 字节。不要提交密码。
read -rs -p 'Administrator password: ' FILE_STATION_PASSWORD
printf '\n'
(umask 077; printf '%s' "$FILE_STATION_PASSWORD" > "$HOME/.config/nixos-file-station/password")
unset FILE_STATION_PASSWORD
```

创建 `$HOME/.config/nixos-file-station/config.json`。将以下所有 `/absolute/home` 替换为 `$HOME` 对应的实际绝对路径；JSON 不展开环境变量。

```json
{
  "ip": "127.0.0.1",
  "port": 8080,
  "stateDir": "/absolute/home/.local/state/nixos-file-station",
  "passwordFile": "/absolute/home/.config/nixos-file-station/password",
  "secureCookies": false,
  "allowedRoots": [
    {"path": "/absolute/home/file-station-data/documents"},
    {"path": "/absolute/home/file-station-data/media", "readOnly": true}
  ],
  "shares": [
    {"id": "documents", "name": "文档", "path": "/absolute/home/file-station-data/documents"},
    {"id": "media", "name": "媒体", "path": "/absolute/home/file-station-data/media", "readOnly": true}
  ]
}
```

```sh
chmod 600 "$HOME/.config/nixos-file-station/config.json" "$HOME/.config/nixos-file-station/password"
/tmp/nixos-file-station -config "$HOME/.config/nixos-file-station/config.json" -static ./web/dist
```

打开 <http://127.0.0.1:8080>。程序启动时必须能找到前端构建产物；上面的 `-static` 指向本地构建目录。

## 配置规则

### 监听与密码

- `ip` 和 `port` 必填，端口范围为 `1–65535`。`ip` 使用 IPv4 或 IPv6 地址，IPv6 不加方括号。`127.0.0.1` 仅本机访问，`0.0.0.0` 监听所有 IPv4 接口。
- `passwordFile` 指向运行时密码文件，读取时去除末尾换行。也可改用 `password` 字符串，但两者必须二选一；密码至少 6 字节，其他空格按原样使用。
- 配置和密码文件必须位于所有允许根目录之外，权限设为 `0600`，不要提交到仓库或写入 Nix store。正式部署推荐使用密码文件；NixOS 模块只接受密码文件。
- `secureCookies` 默认 `false`；通过 HTTPS 访问时设置为 `true`。

### 允许目录与持久状态

- `allowedRoots` 必填且非空，是部署方允许管理员使用的目录边界。每项包含 `path` 和可选的 `readOnly`（默认 `false`）。
- 允许根目录必须是已经存在、互不重叠的规范化绝对目录：不能为 `/`，不能有尾部 `/`、重复分隔符、`.` 或 `..` 路径段，也不能经过符号链接。
- 共享可使用允许根目录或其子目录，但共享之间不能重叠；只读根目录内的共享不能设为可写。服务账户必须有相应的读取、目录遍历及写入权限。
- `stateDir` 必填，使用固定、私有的持久状态绝对路径，与所有允许根目录互不重叠。目录权限应为 `0700`；不要放在 `/tmp`，不要在重新部署时删除。共享设置、任务、会话及回收站元数据保存在其中的 SQLite 数据库中。

### 共享管理与重启

`shares` 是可选的首次导入配置，可为空。每项包含 `name`、`path`，以及可选的 `id`、`readOnly`。仅在状态数据库首次初始化时导入一次；即使最初为空，之后修改该配置、重启、重新部署或回滚 NixOS generation 都不会重新导入。已有部署应保留共享 ID 和状态数据库，避免破坏任务及回收站元数据关联。

后续通过页面的「共享设置」新增、编辑与移除共享，保存立即生效并持久化。移除共享只删除配置，不删除目录或文件。以下情况会返回 HTTP 409，需先解除冲突：

- 任何文件请求（包括上传、下载）正在处理时，所有共享保存与移除都会暂时被阻止。
- 受影响共享有活动后台任务时，不能保存或移除，包括仅修改名称；等待任务结束或取消后再操作。
- 受影响共享的回收站未清空时，不能变更路径或移除；先恢复或永久删除其中的条目。

修改 `allowedRoots`、监听地址、端口、密码或其他运行时配置后需要重启；端口变更后同步修改访问 URL。页面保存共享设置不需要重启。

收紧允许范围、改为只读或目录暂时不可访问时，不符合条件的已保存共享会保留并标记为不可用，文件操作关闭，不会自动删除或迁移。可在「共享设置」查看原因并修正路径或权限；初始 `shares` 不会覆盖这些记录。

## Nix 打包

```sh
nix build path:.#default
./result/bin/nixos-file-station -config "$HOME/.config/nixos-file-station/config.json"
```

flake 支持 `x86_64-linux` 和 `aarch64-linux`。包内包含前端静态资源，运行时无需 Node.js，也无需另外指定 `-static`。

## NixOS 服务

在系统 flake 中加入本项目的 input，并导入 `nixosModules.default`。系统需已配置非 root 用户 `nixos`；模块复用该账户，不创建或修改其登录配置。以下示例使用已有的 `configuration.nix`；将主机名和系统架构替换为实际值，已有 flake 可合并对应的 input 和 module。

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    file-station.url = "github:CYL96/NixOS-File-Station";
    file-station.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = inputs@{ nixpkgs, ... }: {
    nixosConfigurations.file-station = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ./configuration.nix
        inputs.file-station.nixosModules.default
        {
          users.groups.documents = {};
          # 为新部署预先创建目录；不会递归修改已有数据的权限。
          systemd.tmpfiles.rules = [
            "d /srv/documents 2770 root documents - -"
            "d /srv/archive 2770 root documents - -"
          ];

          services.nixos-file-station = {
            enable = true;
            ip = "127.0.0.1";
            port = 8080;
            passwordFile = "/run/secrets/nixos-file-station-password";
            stateDir = "/var/lib/nixos-file-station";
            supplementaryGroups = [ "documents" ];
            allowedRoots = [
              { path = "/srv/documents"; }
              { path = "/srv/archive"; readOnly = true; }
            ];
            # 仅首次初始化时导入；之后在页面管理。
            shares = {
              documents = { name = "文档"; path = "/srv/documents"; };
              archive = { name = "归档"; path = "/srv/archive"; readOnly = true; };
            };
          };
        }
      ];
    };
  };
}
```

部署前通过秘密管理工具或管理员操作提供 `/run/secrets/nixos-file-station-password`，密码要求与本地运行相同。该文件可以保持 root:root、`0600`；模块通过 systemd `LoadCredential` 传递给运行用户 `nixos`，服务主组仍为专用组 `nixos-file-station`，`supplementaryGroups` 仅为服务进程追加组权限。服务与该账户的其他进程共享 UID，不再具有独立用户的隔离边界。**必须使用运行时路径字符串，不要用 Nix 路径字面量或 `writeText` 把密码放进 Nix store。** 修改密码后重启服务。

允许根目录和首次导入的共享目录必须在服务启动前存在。示例的 tmpfiles 规则适用于新建专用目录；已有共享数据应由管理员按需配置组权限或 ACL，不要直接递归 `chown`。只读目录也需要服务用户具备读取和目录遍历权限。模块以 `0700` 创建状态目录，并将状态目录及其内容的属主、属组设为 `nixos:nixos-file-station`，使原有状态可继续使用；不改变已有状态文件的权限位。保留该目录才能保留共享设置、任务和回收站元数据。也可设置 `shares = {};`，首次登录后再添加共享。

`allowedRoots` 同时约束应用允许范围及 systemd 文件系统访问权限。共享级 `readOnly` 由应用强制执行；需要操作系统层面的只读保护时，应在相应允许根目录设置 `readOnly = true`，该根目录内所有共享都必须只读。

```sh
sudo nixos-rebuild switch --flake /path/to/system-flake#file-station
systemctl status nixos-file-station
journalctl -u nixos-file-station
sudo systemctl restart nixos-file-station
```

## HTTPS、反向代理与防火墙

局域网部署推荐通过 HTTPS 反向代理访问，并设置 `secureCookies = true`；NixOS 对应 `services.nixos-file-station.secureCookies = true`。只监听回环地址时，应让代理连接该本地地址。

- 代理应保留原始 Host，前端与接口保持同源，不要拆分到不同源部署。
- 后台事件使用 SSE：关闭 `/api/events` 的代理缓冲，并设置足够长的连接超时。
- 按需配置代理的上传大小限制。
- NixOS 模块不会自动开放防火墙。使用反向代理时开放代理的 HTTPS 端口；若直接监听局域网地址，则按需开放服务的 TCP 端口，并限制可信访问范围。

## 文件操作限制与备份安全

- 路径限制不能替代操作系统隔离。仅配置可信目录，结合服务账户权限及 systemd 沙箱使用；挂载点和已有硬链接仍可能暴露其他数据。不能删除共享根目录，也不能操作范围外或不可用的共享；内部 `.webui-*` 路径不允许通过普通文件操作访问。
- 符号链接可以显示，但不能进入、下载、预览或复制；移动、重命名和回收站操作处理链接本身。仅操作普通文件和目录，不处理特殊文件。
- 复制不保留 Unix 所有者、ACL、扩展属性、硬链接关系或原始时间。新文件模式为 `0640`、目录为 `0750`，仍受部署的 UMask/ACL 影响。同名目录不能递归覆盖。
- 上传和复制使用目标目录内的隐藏临时文件，不静默覆盖。取消操作不会回滚已经完成的条目；异常中断后可能残留临时文件，确认无任务运行后再由管理员清理。
- 文本编辑限已有的、不含 NUL 且不超过 1 MiB 的 UTF-8 普通文件。只读共享、回收站、符号链接、无宿主写权限或多硬链接的文件不能保存。保存需保留权限、所有者、组、ACL 和扩展属性，无法完整保留时拒绝保存；原子替换会改变 inode 和原始时间。
- 文本保存会检查读取版本；检测到外部修改时保留草稿并报告冲突，不提供强制覆盖。但版本检查与替换之间仍可能被外部进程写入，**不是文件系统级 CAS，不能保证绝对避免并发覆盖**。统一 CRLF 文本保存时保留 CRLF，其他换行在编辑后统一为 LF。
- 同文件系统移动使用原子重命名；跨文件系统移动先复制后删除源，**不是整体事务**。失败后可能同时存在源和目标，需检查后再决定如何重试。
- 后台任务的完成和失败状态会持久化；重启时未完成任务标记为中断，不自动重放。
- 回收站位于各共享根目录的 `.webui-trash`，占用同一文件系统空间，元数据保存在 `stateDir`。**回收站不是备份**；备份与恢复应同时包含共享文件、隐藏回收站和状态数据库，并在停止服务及其他写入后保持数据一致。
- 文本不作为网页执行；Markdown 预览不执行原始 HTML，外部图片不自动加载。会话 Cookie 使用 HttpOnly 和 SameSite=Strict，HTTPS 部署应启用 Secure。
- **NixOS generation 回滚不会回滚文件删除、覆盖或状态数据库变更。**
