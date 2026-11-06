package tools

// 工具的入参 Schema（移植自 packages/tools/src/schemas.ts）。
// 引擎/前端只按名字与描述展示；这里给出与契约结构体一致的 JSON Schema 形状。

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringSchema(description string) map[string]any {
	s := map[string]any{"type": "string"}
	if description != "" {
		s["description"] = description
	}
	return s
}

func numberSchema(description string) map[string]any {
	s := map[string]any{"type": "number"}
	if description != "" {
		s["description"] = description
	}
	return s
}

func boolSchema(description string) map[string]any {
	s := map[string]any{"type": "boolean"}
	if description != "" {
		s["description"] = description
	}
	return s
}

func arraySchema(items map[string]any, description string) map[string]any {
	s := map[string]any{"type": "array", "items": items}
	if description != "" {
		s["description"] = description
	}
	return s
}

var (
	schemaIsoWeek = objectSchema(map[string]any{
		"year": map[string]any{"type": "integer"},
		"week": map[string]any{"type": "integer", "minimum": 1, "maximum": 53},
	}, "year", "week")

	schemaAttachment = objectSchema(map[string]any{
		"name": stringSchema("附件名（含扩展名）"),
		"path": stringSchema("工作区内的绝对路径"),
	}, "name", "path")

	schemaValidateSubmission = objectSchema(map[string]any{
		"period":              schemaIsoWeek,
		"attachments":         arraySchema(schemaAttachment, "表单附件"),
		"allowMissingMinutes": map[string]any{"type": []string{"boolean", "null"}, "description": "无纪要继续"},
		"allowPartialPeriod":  map[string]any{"type": []string{"boolean", "null"}, "description": "按已有数据继续"},
	}, "period", "attachments")

	schemaLoadSales = objectSchema(map[string]any{
		"salesPath": stringSchema("销售明细文件路径（xlsx 或 csv）"),
		"outDir":    stringSchema("数据集输出目录"),
	}, "salesPath", "outDir")

	schemaComputeMetrics = objectSchema(map[string]any{
		"datasetPath":      stringSchema("load_sales 产出的数据集文件"),
		"period":           schemaIsoWeek,
		"anomalyThreshold": map[string]any{"type": "number", "exclusiveMinimum": 0},
	}, "datasetPath", "period", "anomalyThreshold")

	schemaClaim = objectSchema(map[string]any{
		"subject": objectSchema(map[string]any{
			"level": map[string]any{"type": "string", "enum": []string{"company", "region", "store", "category"}},
			"key":   map[string]any{"type": "string", "minLength": 1},
		}, "level", "key"),
		"metric":     map[string]any{"type": "string", "enum": []string{"wow"}},
		"direction":  map[string]any{"type": "string", "enum": []string{"flat", "up", "down"}},
		"magnitude":  numberSchema("纪要给出的幅度（小数，0.3 = 30%）"),
		"sourceWeek": schemaIsoWeek,
		"quote":      map[string]any{"type": "string", "minLength": 1},
	}, "subject", "metric", "direction", "sourceWeek", "quote")

	schemaDetectContradictions = objectSchema(map[string]any{
		"claims": arraySchema(schemaClaim, "纪要里的可核验说法"),
		"facts":  map[string]any{"type": "object", "description": "compute_metrics 的输出（WeeklyFacts）"},
		"thresholds": objectSchema(map[string]any{
			"flatTolerance":      map[string]any{"type": "number", "minimum": 0},
			"directionMin":       map[string]any{"type": "number", "minimum": 0},
			"magnitudeTolerance": map[string]any{"type": "number", "minimum": 0},
		}, "flatTolerance", "directionMin", "magnitudeTolerance"),
	}, "claims", "facts", "thresholds")

	schemaParseMinutes = objectSchema(map[string]any{
		"paths": arraySchema(stringSchema("docx 纪要路径"), "纪要文件列表"),
	}, "paths")

	schemaRenderReport = objectSchema(map[string]any{
		"report":         map[string]any{"type": "object", "description": "已渲染槽位的报告（RenderedReport）"},
		"facts":          map[string]any{"type": "object", "description": "compute_metrics 的输出（WeeklyFacts）"},
		"outDir":         stringSchema("交付物输出目录"),
		"idempotencyKey": map[string]any{"type": "string", "minLength": 1},
	}, "report", "facts", "outDir", "idempotencyKey")

	schemaWebSearch = objectSchema(map[string]any{
		"query": stringSchema("搜索词"),
	}, "query")

	schemaAdminSetFaults = objectSchema(map[string]any{
		"tools": map[string]any{
			"type":                 "object",
			"additionalProperties": map[string]any{"type": "string", "enum": []string{"timeout", "error", "invalid_output"}},
			"description":          "工具名 → 故障模式",
		},
	}, "tools")
)
