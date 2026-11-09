// shell 确认卡里的命令块：等宽原样显示将要运行的命令、description 和工作目录，下面是醒目的风险警告条。
// 警告是尽力而为的字符串识别，不是安全边界——卡片上也这么写，免得让人误以为“没警告就安全”。
import { WarningFilled } from "@ant-design/icons";

export function ShellCommandBlock({ command, workdir, description, warnings }: { command: string; workdir: string; description?: string; warnings: string[] }) {
  return (
    <div className="shell-block">
      {description && <div className="card-body">说明：{description}</div>}
      {workdir && (
        <div className="card-body chat-sub">
          工作目录：<code>{workdir}</code>
        </div>
      )}
      <pre className="shell-command" aria-label="将要运行的命令">{command}</pre>
      {warnings.map((text) => (
        <div key={text} className="shell-warning" role="alert">
          <WarningFilled /> {text}
        </div>
      ))}
      <div className="shell-note chat-sub">命令行没有沙箱，会以本程序的用户身份运行。上面的提示只是尽力而为，不是安全边界：请自己看一遍命令再决定。</div>
    </div>
  );
}
