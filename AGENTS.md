# Repository Guidelines

- 本文件只保留帮助 agent 修改仓库的持久指引：架构、代码约定、工具链和验证方式。
- `README.md` 面向使用者，只介绍项目功能、安装配置、使用、部署和安全限制。
- 两者均不得写入任务日志、进度、验证结果、临时发现、本机部署记录或无关细节；执行结果仅在对话中汇报。

## Project Overview

NixOS File Station 是单管理员 Web 文件管理器。Go 服务提供 API 和 React/TypeScript 静态页面；SQLite 保存应用状态，文件内容留在共享目录。运行配置和使用说明见 `README.md`。

## Architecture & Data Flow

- `cmd/webui/main.go` 读取 JSON 配置，调用 `server.New(Config, staticDir)`，负责服务启动与关闭。
- 后端集中在 `internal/server` 包。`Server` 持有配置、数据库、根目录句柄和任务状态；通过构造函数传入配置，不使用 DI 容器。
- 前端由 `web/src/main.tsx` 挂载 `App.tsx`：全局状态留在 App，组件通过 props/callbacks 协作，局部状态留在组件；没有独立状态库或路由库。
- `web/src/api.ts` 统一处理 JSON 请求、会话、CSRF 和错误；`api()` / `shareAPI()` 包装后端处理器。后台操作写入任务状态，SSE 推送更新，前端轮询兜底。
- 文件接口以共享 ID 和相对路径访问 `os.Root`；`allowedRoots` 是部署边界。配置中的 `shares` 仅首次导入，后续共享设置以 `stateDir/state.sqlite` 为准。

## Key Directories

- `internal/server/`：按领域拆分文件的单一 Go 包，测试与实现同目录。
- `web/src/`：功能组件、API 客户端、hooks 和 `styles.css`；共享 UI 原语在 `ui.tsx`。
- `nix/`：前后端打包、NixOS 服务模块和 Go vendor hash。

## Development Commands

在仓库根目录执行：

```sh
nix develop path:.                         # 可选，提供所需工具链
npm --prefix web ci
npm --prefix web run build                 # tsc -b + Vite
go build -o /tmp/nixos-webui ./cmd/webui
/tmp/nixos-webui -config "$HOME/.config/nixos-webui/config.json" -static ./web/dist
npm --prefix web run dev                   # 另开终端，前端热更新
go test -race ./...
go vet ./...
nix build path:.#default
```

先按 README 创建配置和私有密码文件。服务拒绝 root，启动前必须有静态构建产物。Vite 默认代理到 `127.0.0.1:8080`，后端地址不同时需调整 `web/vite.config.ts`。Nix 包自动指定静态资源路径。

## Code Conventions & Common Patterns

- Go 使用 `gofmt`。处理器返回 `error`，复用 `fail()` / `errorResponse()`；JSON 请求复用有大小限制、拒绝未知字段的 `decode()`。通过既有包装器注册接口，不能绕过鉴权、CSRF 或共享锁。
- 文件操作复用 `validName`、`safePath`、`noLinks` 和 `os.Root`，不拼接用户输入访问宿主路径。保留只读检查、`.webui-*` 内部命名空间、变更互斥、暂存后发布及取消清理。
- TypeScript 使用两空格、单引号、分号；组件文件为 PascalCase，hook 为 `useXxx.ts`。样式使用现有 CSS、kebab-case 类名和 CSS 变量。界面及用户错误文案用中文。
- Go JSON 字段与 `web/src/api.ts` 类型手动同步。JSON 使用 `request<T>()`，预览和上传复用专用 helper；通过 `ApiError`、组件错误状态或通知展示失败，401 沿用统一会话清理。
- 异步逻辑沿用 effect 级 `AbortController`、会话 epoch 防止旧响应回写，以及串行弹框/上传队列；不要另建重复的请求或状态机制。

## Important Files

- `internal/server/server.go`：配置、初始化、数据库建表、路由和公共中间件。
- `internal/server/shares.go`：目录边界、共享持久化和锁。
- `internal/server/tasks.go`：后台任务、取消、SSE 和回收站。
- `internal/server/file_text.go`：版本冲突检查与保留元数据的原子文本保存。
- `web/src/App.tsx`、`web/src/api.ts`：交互编排及前后端契约。
- `nix/package.nix`、`nix/module.nix`：打包和服务权限/沙箱配置。

## Runtime/Tooling Preferences

使用 Linux、Go 1.26、Node.js 24 和 npm；依赖以 `go.mod` / `go.sum`、`web/package-lock.json` 为准，不切换包管理器。flake 支持 x86_64-linux、aarch64-linux；打包使用纯 Go SQLite、`CGO_ENABLED=0`，运行时不需要 Node。

Go 依赖变更后执行 `go mod tidy`，并将 `nix/vendor-hash.nix` 更新为 Nix 报告的真实 hash；不得提交占位 hash。前端打包使用 `importNpmLock`。不提交私有配置、密码、SQLite 或生成产物；状态和密码必须在允许根目录之外，秘密不得进入 Nix store。不得通过删除状态库重置设置。

## Testing & QA

- 后端沿用标准 `testing`、`httptest.NewServer`、cookie jar 和 `t.TempDir()`，使用真实临时文件及 SQLite。扩展相邻 `*_test.go`，覆盖可观察行为与边界，尤其路径/符号链接、权限、只读、CSRF、取消、冲突及持久化。
- 执行 `go test -race ./...`、`go vet ./...` 和前端 build；打包或依赖变更另运行 Nix build。Nix 包只指定 `cmd/webui`，不能代替服务测试。
- 前端没有测试 runner、独立 lint 脚本或覆盖率门槛；严格 TypeScript 检查包含在 build 中。UI 修改需在真实后端上用浏览器验证受影响流程及窄屏布局，不能只以打包成功作为行为证明。
