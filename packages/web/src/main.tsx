import { createRoot } from "react-dom/client";
import dayjs from "dayjs";
import "dayjs/locale/zh-cn";
import { App } from "./App";
import { installZoomGuard } from "./view/zoom-guard";
import "./styles.css";

// 日期控件跟随中文习惯：周一是一周的第一天，周选择器的一行正好是一个 ISO 周
dayjs.locale("zh-cn");

// 桌面工作台不让页面缩放（Ctrl+滚轮、Ctrl+加减号/0、触控板捏合）；字号在“设置 → 外观”里调
installZoomGuard(window);

createRoot(document.getElementById("root")!).render(<App />);
