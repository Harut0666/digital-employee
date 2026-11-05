// Bun 的文件导入（`with { type: "file" }`）在类型层面声明为字符串路径。
declare module "*.wasm" {
  const path: string;
  export default path;
}
