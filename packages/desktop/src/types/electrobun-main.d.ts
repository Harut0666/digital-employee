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
    hidden?: boolean;
    navigationRules?: string[] | null;
  }

  /** SDK 事件对象：ElectrobunEvent<DataType, ResponseType> 的最小形状 */
  export interface ElectrobunEvent<T = unknown> {
    name: string;
    data: T;
    response?: unknown;
  }

  export interface WebviewNavigateDetail {
    url?: string;
    allowed?: boolean;
  }

  export interface WebviewNavigateData {
    detail?: WebviewNavigateDetail | string;
  }

  export interface WebviewNewWindowDetail {
    url?: string;
    isCmdClick?: boolean;
  }

  export interface WebviewNewWindowData {
    detail?: WebviewNewWindowDetail | string;
  }

  export interface WindowResizeData {
    width: number;
    height: number;
    x?: number;
    y?: number;
  }

  export class BrowserView {
    readonly id: number;
    loadURL(url: string): void;
    executeJavascript(js: string): void;
    openDevTools(): void;
    setNavigationRules(rules: string[]): void;
    on(name: string, handler: (event: ElectrobunEvent) => void): void;
  }

  export class BrowserWindow {
    constructor(options?: BrowserWindowOptions);
    readonly id: number;
    readonly webview: BrowserView;
    on(name: string, handler: (event: ElectrobunEvent) => void): void;
    close(): unknown;
    show(): unknown;
    hide(): unknown;
    setSize(width: number, height: number): unknown;
    getSize(): { width: number; height: number };
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
    openExternal(url: string): boolean;
    showMessageBox(options?: MessageBoxOptions): Promise<MessageBoxResponse>;
  };

  export interface Rect {
    x: number;
    y: number;
    width: number;
    height: number;
  }

  export interface Display {
    id: number;
    bounds: Rect;
    workArea: Rect;
    scaleFactor: number;
    isPrimary: boolean;
  }

  export const Screen: {
    getPrimaryDisplay(): Display;
  };

  export interface ApplicationMenuItemConfig {
    type?: "normal" | "divider" | "separator";
    label?: string;
    tooltip?: string;
    action?: string;
    role?: string;
    data?: unknown;
    submenu?: ApplicationMenuItemConfig[];
    enabled?: boolean;
    checked?: boolean;
    hidden?: boolean;
    accelerator?: string;
  }

  export interface ApplicationMenuClickedData {
    id?: number;
    action: string;
    data?: unknown;
  }

  export const ApplicationMenu: {
    setApplicationMenu(menu: ApplicationMenuItemConfig[]): void;
    on(
      name: "application-menu-clicked",
      handler: (event: ElectrobunEvent<ApplicationMenuClickedData>) => void,
    ): void;
  };

  export interface BuildConfigType {
    mainProcess?: "bun" | "cottontail" | "zig" | "rust" | "go" | "odin";
    defaultRenderer: "native" | "cef";
    availableRenderers: ("native" | "cef")[];
    buildEnvironment?: "dev" | "canary" | "stable";
    /** dev 构建为 false；stable / canary 为 true */
    isPackaged: boolean;
    channel: "dev" | "canary" | "stable";
    runtime?: Record<string, unknown>;
  }

  export const BuildConfig: {
    getSync(): BuildConfigType;
  };

  export interface ElectrobunApp {
    events: {
      on(name: string, handler: (event: unknown) => void): void;
    };
  }

  const Electrobun: ElectrobunApp;
  export default Electrobun;
}
