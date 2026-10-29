// 查看源文件：直接编辑原文件（供开发人员修复杂改动）；保存时仍然先校验再写回。
import { useEffect, useState } from "react";
import { Alert, Input, Select, Space, Tag, Typography } from "antd";
import { fileOf, textOf, type TabProps } from "./helpers";

export function SourceFilesView({ config, drafts, setContent }: TabProps) {
  const [path, setPath] = useState<string>(config.files[0]?.path ?? "workflow.yaml");

  useEffect(() => {
    if (!config.files.some((file) => file.path === path)) setPath(config.files[0]?.path ?? "workflow.yaml");
  }, [config, path]);

  const file = fileOf(config, path);
  return (
    <Space direction="vertical" size={12} style={{ width: "100%" }}>
      <Alert
        type="info"
        showIcon
        message="源文件视图直接编辑原文，保存时先校验再写回；YAML 按你输入的文本保存，注释不会丢。"
        description="表单页保存时会保留原文件的注释；这里适合做嵌套字段、步骤顺序等复杂改动。"
      />
      <Space wrap>
        <Select
          style={{ width: 340 }}
          value={path}
          options={config.files.map((f) => ({ value: f.path, label: f.path }))}
          onChange={setPath}
        />
        {file ? <Tag>{file.kind}</Tag> : null}
        {file ? (
          <Typography.Text type="secondary" code>
            {new TextEncoder().encode(file.content).length} 字节
          </Typography.Text>
        ) : null}
      </Space>
      {file?.error ? <Alert type="error" showIcon message={file.error} /> : null}
      <Input.TextArea
        rows={26}
        spellCheck={false}
        style={{ fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace", fontSize: 13 }}
        value={textOf(config, drafts, path)}
        onChange={(event) => setContent(path, event.target.value)}
        placeholder="选择要编辑的配置文件"
      />
    </Space>
  );
}
