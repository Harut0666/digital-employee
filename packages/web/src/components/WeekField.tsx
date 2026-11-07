// 周选择器：表单界面配置（form.ui.json）里给 {year, week} 字段配 "ui:field": "isoWeek" 时使用。
// 选任意一天得到它所在的 ISO 周，并显示“10 月 26 日至 11 月 1 日”，不用自己算周次。

import { DatePicker } from "antd";
import dayjs, { type Dayjs } from "dayjs";
import type { FieldProps, RegistryFieldsType } from "@rjsf/utils";
import { formatWeekRange, isoWeekMonday, isoWeekOf } from "../view/iso-week";

interface WeekValue {
  year?: number;
  week?: number;
}

const toDayjs = (v: WeekValue | undefined): Dayjs | null => {
  if (!v?.year || !v.week) return null;
  const monday = isoWeekMonday(v.year, v.week);
  return dayjs(new Date(monday.getUTCFullYear(), monday.getUTCMonth(), monday.getUTCDate()));
};

export function WeekField(props: FieldProps) {
  const value = props.formData as WeekValue | undefined;
  const title = props.uiSchema?.["ui:title"] ?? props.schema.title ?? props.name;
  const picked = value?.year && value.week ? value : undefined;
  const errors = props.errorSchema?.__errors ?? [];
  return (
    <div className="week-field">
      <div className="week-field-label">
        {props.required && <span style={{ color: "var(--danger)", marginRight: 4 }}>*</span>}
        {String(title)}
      </div>
      <DatePicker
        picker="week"
        style={{ width: "100%" }}
        disabled={props.disabled || props.readonly}
        value={toDayjs(value)}
        format={(d) => {
          const w = isoWeekOf(d.toDate());
          return `${w.year} 年第 ${w.week} 周`;
        }}
        placeholder="选择周"
        onChange={(d) => props.onChange(d ? isoWeekOf(d.toDate()) : undefined, props.fieldPathId.path, undefined, props.fieldPathId.$id)}
      />
      {/* 字段说明由 @rjsf 的字段模板显示，这里只给日期范围 */}
      {picked && <div className="week-field-range">{formatWeekRange(picked.year!, picked.week!)}</div>}
      {errors.map((e) => (
        <div key={e} style={{ color: "var(--danger)", fontSize: 12 }}>
          {e}
        </div>
      ))}
    </div>
  );
}

/** form.ui.json 用 ui:field 选用的自定义控件，所有渲染员工表单的地方都要传给 @rjsf 的 fields */
export const FORM_FIELDS: RegistryFieldsType = { isoWeek: WeekField };
