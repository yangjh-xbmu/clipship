# SESSION LOG

## 完成
- 2026-04-19 调研 Superpowers 工作流并评估 clipship 支持任意文件剪贴板的可行性（约 2-3 天工作量，无结构性难点）
- 2026-04-19 将 obra/superpowers 的 14 个 skills vendor 到 clipship 项目级 .claude/skills/，并在 .gitignore 排除
- 2026-04-19 对 clipship 仓库做敏感信息脱敏：用 git filter-repo 将 README/config 示例中的真实 Tailscale 机器名、用户名、SSH key 路径替换为占位符，工作区 + 4 个历史提交全部重写，force push 覆盖远端
- 2026-04-19 清理脱敏过程的备份目录和临时替换规则文件

## 发现
- 2026-04-19 obra/superpowers 的 slash commands（/brainstorm、/write-plan、/execute-plan）已全部 deprecated，只是壳提示用户改调同名 skill；真正入口是 skills 目录
- 2026-04-19 superpowers 的 hooks 依赖 $CLAUDE_PLUGIN_ROOT 环境变量，手动 vendor 到非 plugin 目录会失效，只能通过 /plugin install 正式安装才生效
- 2026-04-19 Claude Code 的可用 skill 列表在 session 启动时固化到 system prompt，中途往项目级 .claude/skills/ 里新加的 skill 当前 session 不可见，必须 /clear 或新开 session 才能被 Skill 工具发现
- 2026-04-19 跨平台『复制文件』本质都是复制路径而非文件内容（Windows CF_HDROP、macOS public.file-url、Linux text/uri-list），因此文件剪贴板支持的复杂度集中在协议和路径解析，文件流式处理反而不是问题
- 2026-04-19 git-filter-repo --replace-text 接 '字面==>替换' 规则文件即可同步重写工作区和全部 commit 历史，比 git filter-branch 简单且快（本仓库 4 commit 0.15s 完成）
- 2026-04-19 git-filter-repo 执行后会主动移除 origin 远端作为安全措施，防止误 push；重写完成后需手动 git remote add origin 再 force push
- 2026-04-19 force push 到 main 和 rm -rf 在 Claude Code 权限系统里硬拦，即使 CLAUDE.md 白名单也绕不过，只能用 !命令 让用户在输入框里亲自跑

## 待办
1. 2026-04-19 新 session 里用 Superpowers 工作流（brainstorming → writing-plans → executing-plans → TDD）开发 clipship 文件剪贴板 feature（支持任意文件类型，不只 PNG）
