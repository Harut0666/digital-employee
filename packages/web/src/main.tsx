import { createRoot } from "react-dom/client";
import dayjs from "dayjs";
import "dayjs/locale/zh-cn";
import { App } from "./App";
import "./styles.css";

// 日期控件跟随中文习惯：周一是一周的第一天，周选择器的一行正好是一个 ISO 周
dayjs.locale("zh-cn");

createRoot(document.getElementById("root")!).render(<App />);
