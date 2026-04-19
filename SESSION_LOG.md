# SESSION LOG

## 完成
- 2026-04-19 调研 Superpowers 工作流并评估 clipship 支持任意文件剪贴板的可行性（约 2-3 天工作量，无结构性难点）
- 2026-04-19 将 obra/superpowers 的 14 个 skills vendor 到 clipship 项目级 .claude/skills/，并在 .gitignore 排除
- 2026-04-19 对 clipship 仓库做敏感信息脱敏：用 git filter-repo 将 README/config 示例中的真实 Tailscale 机器名、用户名、SSH key 路径替换为占位符，工作区 + 4 个历史提交全部重写，force push 覆盖远端
- 2026-04-19 清理脱敏过程的备份目录和临时替换规则文件
- 2026-04-19 brainstorming 产出 v0.4 文件剪贴板设计 spec：Windows-first 切片、GET/TYPE/ERR 协议、单文件/多文件/目录分流（tar 打包）、软限制 500 MiB + --force、JSON stdout
- 2026-04-19 writing-plans 产出 16 Task / 3 里程碑实现 plan（TDD 全覆盖）
- 2026-04-19 里程碑 1（Task 1-12）落地：proto 编解码 + pack 打包/sanitize + files 接口与 stub/fake + config 扩展 + server/client/cmd 全面升级到新协议与 JSON stdout
- 2026-04-19 里程碑 2（Task 13-14）落地：Windows CF_HDROP + DragQueryFileW 真实实现 + e2e_windows.ps1 手动 smoke 脚本
- 2026-04-19 里程碑 3（Task 15-16）落地：README 按三工作流重写 + /clip skill 改用 pull-auto + 迁移指南；版本 0.4.0 验证、go vet 干净、darwin/arm64 + windows/amd64 全部 build+test -race 通过

## 发现
- 2026-04-19 obra/superpowers 的 slash commands（/brainstorm、/write-plan、/execute-plan）已全部 deprecated，只是壳提示用户改调同名 skill；真正入口是 skills 目录
- 2026-04-19 superpowers 的 hooks 依赖 $CLAUDE_PLUGIN_ROOT 环境变量，手动 vendor 到非 plugin 目录会失效，只能通过 /plugin install 正式安装才生效
- 2026-04-19 Claude Code 的可用 skill 列表在 session 启动时固化到 system prompt，中途往项目级 .claude/skills/ 里新加的 skill 当前 session 不可见，必须 /clear 或新开 session 才能被 Skill 工具发现
- 2026-04-19 跨平台『复制文件』本质都是复制路径而非文件内容（Windows CF_HDROP、macOS public.file-url、Linux text/uri-list），因此文件剪贴板支持的复杂度集中在协议和路径解析，文件流式处理反而不是问题
- 2026-04-19 git-filter-repo --replace-text 接 '字面==>替换' 规则文件即可同步重写工作区和全部 commit 历史，比 git filter-branch 简单且快（本仓库 4 commit 0.15s 完成）
- 2026-04-19 git-filter-repo 执行后会主动移除 origin 远端作为安全措施，防止误 push；重写完成后需手动 git remote add origin 再 force push
- 2026-04-19 force push 到 main 和 rm -rf 在 Claude Code 权限系统里硬拦，即使 CLAUDE.md 白名单也绕不过，只能用 !命令 让用户在输入框里亲自跑
- 2026-04-19 Go build tag 组合 `windows && !clipship_fake` + `clipship_fake` 能彻底隔离平台真实实现与 fake 实现，让 TDD 在 macOS 开发机全覆盖 Windows 以外的逻辑路径；真实 CF_HDROP 代码只靠 Windows CI 或手动 e2e 脚本回归
- 2026-04-19 daemon 的 wire 协议里 `TYPE file` 只能承载单个文件名，多文件/目录一律打 tar 返回 `TYPE tar`；name 字段用 url.PathEscape 是为了承载空格/Unicode 且不与 SIZE/name 边界冲突
- 2026-04-19 tar 解包时先 `filepath.Rel` 再检查是否逃出 destDir 是比 strings.HasPrefix 更可靠的 path traversal 防护

## 待办
（空）
