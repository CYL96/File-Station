# File Station

- 本文件只保留帮助 agent 修改仓库的持久指引：架构、代码约定、工具链和验证方式。
- `README.md` 面向使用者，只介绍项目功能、安装配置、使用、部署和安全限制。
- 两者均不得写入任务日志、进度、验证结果、临时发现、本机部署记录或无关细节；执行结果仅在对话中汇报。

## Project Overview

File Station 是支持普通 Linux 用户、Docker 和 NixOS 的单管理员 Web 文件管理器。Go 服务提供 API 和 React/TypeScript 静态页面；SQLite 保存应用状态，文件内容留在共享目录。运行配置和使用说明见 `README.md`。

## Architecture & Data Flow

- `cmd/file-station/main.go` 选择可选 JSON 配置和静态目录；`internal/server/config.go` 的 `LoadConfig` 按非空配置路径分离来源：有路径仅加载默认值 + 严格 JSON，完全忽略 `PASSWORD` / `UMASK`（包括空值和非法值）；无路径仅读取这两个业务环境变量，固定监听 `0.0.0.0:8080`、状态 `/var/lib/file-station`、首次零共享。`-config` 优先于大小写精确的 `ConfigPath`，显式空 flag 也优先；静态目录仅使用 `-static`，默认 `web/dist`，与配置来源独立。JSON 默认监听 `127.0.0.1`，`umask` 默认 `"0022"`，必须是 3 或 4 位、值不超过 `0777` 的八进制字符串，显式空值无效。入口调用 `server.New(Config, staticDir)`，拒绝 root 并负责启动与关闭。
- 后端集中在 `internal/server` 包。`Server` 持有配置、数据库、根目录句柄和任务状态；通过构造函数传入配置，不使用 DI 容器。
- 前端由 `web/src/main.tsx` 挂载 `App.tsx`：全局状态留在 App，组件通过 props/callbacks 协作，局部状态留在组件；没有独立状态库或路由库。
- `web/src/api.ts` 统一处理 JSON 请求、会话、CSRF 和错误；`api()` / `shareAPI()` 包装后端处理器。后台操作写入任务状态，SSE 推送更新，前端轮询兜底。
- 文件接口以共享 ID 和相对路径访问 `os.Root`；目录选择器按进程 UID/GID 的实际权限浏览绝对路径，不设 allowedRoots 白名单。共享不得覆盖私有状态或密码。默认零共享，配置中的 `shares` 仅首次导入，后续以 `stateDir/state.sqlite` 为准。

## Key Directories

- `internal/server/`：按领域拆分文件的单一 Go 包，测试与实现同目录。
- `web/src/`：功能组件、API 客户端、hooks 和 `styles.css`；共享 UI 原语在 `ui.tsx`。
- `nix/`：前后端打包、NixOS 服务模块和 Go vendor hash。
- `Dockerfile`：Node.js 24 / Go 1.26 多阶段构建，Alpine 入口短暂以 root 校验 `PUID` / `PGID`（脚本内默认 `1000:1000`，仅允许 `1`–`4294967294` 的十进制整数，无前导零，显式空值报错），仅 chown 状态目录本身，再用 `su-exec` 降权，由 Go 以新身份 chmod `0700`；应用始终非 root，不递归修改状态内容或共享。非 root `--user` 对未设置的 ID 沿用当前身份，显式不匹配的 `PUID` / `PGID` 报错，不自动提权。默认 ID 不放在 Docker ENV 中，避免覆盖非 root 当前身份。`compose.yaml` 仅显式传入 `PUID`、`PGID`、`UMASK`、`PASSWORD`、`ConfigPath`，无 `user:` 或密码 secret；密码无默认值、至少 6 字节，建议 12 字节以上，umask 必须加引号。宿主端口直接写 `127.0.0.1:8080:8080`，持久化 `./state` 并提供可选数据/只读 JSON 挂载。保留 `cap_drop: ALL`、入口所需 `CHOWN` / `SETUID` / `SETGID` 及 `no-new-privileges`，不需要 `FOWNER`。`su-exec` 清空附加组，不宣称 Docker `group_add` 保留。容器选 JSON 后完全忽略 `PASSWORD` / `UMASK`，应明确设置 `ip: "0.0.0.0"`；静态资源由镜像命令 `-static /usr/share/file-station` 指定。

## Development Commands

在仓库根目录执行：

```sh
nix develop path:.                         # 可选，提供所需工具链
npm --prefix web ci
npm --prefix web run build                 # tsc -b + Vite
go build -o /tmp/file-station ./cmd/file-station
/tmp/file-station -config "$HOME/.config/file-station/config.json" -static ./web/dist
npm --prefix web run dev                   # 另开终端，前端热更新
go test -race ./...
go vet ./...
nix build path:.#default
docker build -t file-station:local .
```

先按 README 创建本地配置和私有密码文件，或为 Docker 直接设置 `PASSWORD`。服务拒绝 root，启动前必须有静态构建产物。Vite 默认代理到 `127.0.0.1:8080`，后端地址不同时需调整 `web/vite.config.ts`。Nix 包自动指定静态资源路径；Docker 入口仅准备状态目录本身，更换已有状态的 UID/GID 时须由管理员停服务、备份并安全迁移内容属主，不能删除状态库。

## Code Conventions & Common Patterns

- Go 使用 `gofmt`。处理器返回 `error`，复用 `fail()` / `errorResponse()`；JSON 请求复用有大小限制、拒绝未知字段的 `decode()`。通过既有包装器注册接口，不能绕过鉴权、CSRF 或共享锁。
- 文件操作复用 `validName`、`safePath`、`noLinks` 和 `os.Root`，不拼接用户输入访问宿主路径。保留只读检查、`.webui-*` 内部命名空间、变更互斥、暂存后发布及取消清理。
- 普通新文件使用 `0666 & ~umask`，新目录使用 `0777 & ~umask`；内部状态与暂存保持私有。文本保存、同文件系统移动及回收站恢复保留原内容权限，不将新建权限套用到已有内容。文件模式只使用 JSON `umask`，环境变量模式使用 `UMASK`，默认均为 `"0022"`。
- TypeScript 使用两空格、单引号、分号；组件文件为 PascalCase，hook 为 `useXxx.ts`。样式使用现有 CSS、kebab-case 类名和 CSS 变量。界面及用户错误文案用中文。
- Go JSON 字段与 `web/src/api.ts` 类型手动同步。JSON 使用 `request<T>()`，预览和上传复用专用 helper；通过 `ApiError`、组件错误状态或通知展示失败，401 沿用统一会话清理。
- 异步逻辑沿用 effect 级 `AbortController`、会话 epoch 防止旧响应回写，以及串行弹框/上传队列；不要另建重复的请求或状态机制。

## Important Files

- `internal/server/config.go`：默认值与文件 / 环境变量二选一加载；本地推荐 JSON，NixOS 通过 `-config` 使用文件模式，Docker 默认环境变量模式。密码来源仅在所选模式内校验，不保留跨文件 / env 的密码清除分支。
- `internal/server/server.go`：配置结构、初始化、数据库建表、路由和公共中间件。
- `internal/server/shares.go`：基于系统权限的目录浏览、私有路径保护、共享持久化和锁。
- `internal/server/tasks.go`：后台任务、取消、SSE 和回收站。
- `internal/server/file_text.go`：版本冲突检查与保留元数据的原子文本保存。
- `web/src/App.tsx`、`web/src/api.ts`：交互编排及前后端契约。
- `nix/package.nix`、`nix/module.nix`：file-station 打包、服务用户/组、凭据与系统加固；模块不按共享列表重映射普通文件系统路径。`LoadCredential` 的实际凭据用 `passwordFile` 读取，原路径通过只防共享、不读取或认证的 `passwordSourceFile` 保护；隐私元数据每次启动应用，不同于 `shares` 的一次性导入，切换认证密码来源不自动清除它。固定生成 JSON `umask: "0077"` 并保留 systemd `UMask=0077`，不新增 module 选项。保持 `RemoveIPC=false`，避免清理复用用户或组的其他进程 IPC。

## Runtime/Tooling Preferences

使用 Linux、Go 1.26、Node.js 24 和 npm；依赖以 `go.mod` / `go.sum`、`web/package-lock.json` 为准，不切换包管理器。flake 支持 x86_64-linux、aarch64-linux；打包使用纯 Go SQLite、`CGO_ENABLED=0`，运行时不需要 Node。

Go 依赖变更后执行 `go mod tidy`，并将 `nix/vendor-hash.nix` 更新为 Nix 报告的真实 hash；不得提交占位 hash。前端打包使用 `importNpmLock`。不提交私有配置、密码、SQLite 或生成产物；私有状态和密码必须在共享之外，秘密不得进入 Nix store 或容器镜像层。NixOS 密码只能用运行时绝对路径文件，不得通过明文 Nix 配置或 store 文件传入。本地可保留 `passwordFile`；Docker 默认直接填写 `PASSWORD`，但环境变量和 Compose 明文对 inspect/宿主管理员可见，不宣称保密。Compose YAML 密码中的 `$` 需转义成 `$$`，私有 `.env` 可用单引号避免插值。不得通过删除状态库重置设置。

## Testing & QA

- 后端沿用标准 `testing`、`httptest.NewServer`、cookie jar 和 `t.TempDir()`，使用真实临时文件及 SQLite。扩展相邻 `*_test.go`，覆盖可观察行为与边界，尤其路径/符号链接、权限、只读、CSRF、取消、冲突及持久化。
- 执行 `go test -race ./...`、`go vet ./...` 和前端 build；打包或依赖变更另运行 Nix build，容器变更运行镜像构建与非 root 部署检查。Nix 包只指定 `cmd/file-station`，不能代替服务测试。
- 前端没有测试 runner、独立 lint 脚本或覆盖率门槛；严格 TypeScript 检查包含在 build 中。UI 修改需在真实后端上用浏览器验证受影响流程及窄屏布局，不能只以打包成功作为行为证明。
