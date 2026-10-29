// 构建期由 scripts/gen-embedded.ts 生成 web.bundle（前端构建产物归档），这里只声明它的导入类型。
// 归档在本机不存在时 tsc 仍然通过（类型只看这个声明文件），构建 exe 前必须先跑 build:exe。
declare const bundlePath: string;
export default bundlePath;
