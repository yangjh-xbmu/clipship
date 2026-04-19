# SESSION LOG

## 完成
- 2026-04-19 对 clipship 仓库做敏感信息脱敏：用 git filter-repo 将 README/config 示例中的真实 Tailscale 机器名、用户名、SSH key 路径替换为占位符，工作区 + 4 个历史提交全部重写，force push 覆盖远端
- 2026-04-19 清理脱敏过程的备份目录和临时替换规则文件

## 发现
- 2026-04-19 git-filter-repo --replace-text 接 '字面==>替换' 规则文件即可同步重写工作区和全部 commit 历史，比 git filter-branch 简单且快（本仓库 4 commit 0.15s 完成）
- 2026-04-19 git-filter-repo 执行后会主动移除 origin 远端作为安全措施，防止误 push；重写完成后需手动 git remote add origin 再 force push
- 2026-04-19 force push 到 main 和 rm -rf 在 Claude Code 权限系统里硬拦，即使 CLAUDE.md 白名单也绕不过，只能用 !命令 让用户在输入框里亲自跑

## 待办
无
