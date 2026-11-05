// 手写的 Electrobun 2.0.6 主进程最小类型声明。
// 只用到这里真正调用的 API；真实 SDK 类型由 Hutch 的 devkit 生成，构建时不参与 tsc。
// 目的：不安装 Hutch 的机器上 `bun run typecheck` 也能通过（Spec 0003 的关键约定）。
declare module "electrobun/main" {
  export interface BrowserWindowOptions {
    title?: string;
    url?: string | null;
    html?: string | null;
    frame?: { x?: number; y?: number; width: number; height: number };
    sandbox?: boolean;
  }

  export interface ElectrobunWindowEvent {
    name: string;
    data?: unknown;
  }

  export class BrowserWindow {
    constructor(options?: BrowserWindowOptions);
    readonly id: number;
    on(name: string, handler: (event: ElectrobunWindowEvent) => void): void;
    close(): unknown;
  }

  export const PATHS: {
    RESOURCES_FOLDER: string;
    VIEWS_FOLDER: string;
  };

  export interface MessageBoxOptions {
    type?: "info" | "warning" | "error" | "question";
    title?: string;
    message: string;
    detail?: string;
    buttons?: string[];
    defaultId?: number;
    cancelId?: number;
  }

  export interface MessageBoxResponse {
    response: number;
  }

  export const Utils: {
    quit(code?: number): boolean;
    openPath(path: string): boolean;
    showMessageBox(options?: MessageBoxOptions): Promise<MessageBoxResponse>;
  };

  export interface ElectrobunApp {
    events: {
      on(name: string, handler: (event: unknown) => void): void;
    };
  }

  const Electrobun: ElectrobunApp;
  export default Electrobun;
}
