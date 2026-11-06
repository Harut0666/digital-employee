package contract

// 场景 A 的事实（facts.ts）。金额一律整数“分”，比例一律小数（0.123 = 12.3%）。
// 交付物里每个数字都必须来自这里：模型只能用 {{path}} 槽位引用（ADR-0002）。

// IsoWeek ISO 周。
type IsoWeek struct {
	Year int `json:"year" yaml:"year"`
	Week int `json:"week" yaml:"week"`
}

// PeriodRange 统计周期：周一 from / 周日 to。
type PeriodRange struct {
	IsoWeek `yaml:",inline"`
	From    string `json:"from" yaml:"from"`
	To      string `json:"to" yaml:"to"`
}

// Metric 一个范围（全公司/区域）的指标。不可比/无数据的字段为 nil，序列化成 null。
type Metric struct {
	RevenueCents     int64    `json:"revenueCents"`
	PrevRevenueCents *int64   `json:"prevRevenueCents"`
	Wow              *float64 `json:"wow"`
	GrossMarginRate  *float64 `json:"grossMarginRate"`
	TargetCents      *int64   `json:"targetCents"`
	Achievement      *float64 `json:"achievement"`
	DeliveryShare    *float64 `json:"deliveryShare"`
}

// StoreType 门店类型。
type StoreType string

const (
	StoreMall      StoreType = "商场店"
	StoreStreet    StoreType = "街边店"
	StoreCommunity StoreType = "社区店"
	StoreOffice    StoreType = "写字楼店"
)

// StoreMetric 门店指标。
type StoreMetric struct {
	Metric `yaml:",inline"`
	Code   string    `json:"code"`
	Name   string    `json:"name"`
	Region string    `json:"region"`
	Type   StoreType `json:"storeType"`
}

// CategoryMetric 品类指标。
type CategoryMetric struct {
	Category         string   `json:"category"`
	RevenueCents     int64    `json:"revenueCents"`
	PrevRevenueCents *int64   `json:"prevRevenueCents"`
	Wow              *float64 `json:"wow"`
}

// RegionCategoryMetric 某区域某品类本周与上周的实收、毛利率。
type RegionCategoryMetric struct {
	RevenueCents        int64    `json:"revenueCents"`
	PrevRevenueCents    *int64   `json:"prevRevenueCents"`
	GrossMarginRate     *float64 `json:"grossMarginRate"`
	PrevGrossMarginRate *float64 `json:"prevGrossMarginRate"`
}

// ReturnGroup 一笔具名退货：同一门店、同一日期、同一单品合并成一组。
type ReturnGroup struct {
	Store        string `json:"store"`
	StoreName    string `json:"storeName"`
	Date         string `json:"date"`
	Product      string `json:"product"`
	ProductName  string `json:"productName"`
	Count        int    `json:"count"`
	RevenueCents int64  `json:"revenueCents"`
}

// Anomaly 环比超过阈值的区域或门店。
type Anomaly struct {
	ID                     string    `json:"id"`
	Level                  string    `json:"level"` // region | store
	Key                    string    `json:"key"`
	Name                   string    `json:"name"`
	Region                 string    `json:"region"`
	StoreType              StoreType `json:"storeType,omitempty"`
	Wow                    float64   `json:"wow"`
	RevenueCents           int64     `json:"revenueCents"`
	PrevRevenueCents       int64     `json:"prevRevenueCents"`
	BaseWeekHasMissingDays bool      `json:"baseWeekHasMissingDays"`
}

// StoreDay 未营业/无数据的门店日。
type StoreDay struct {
	Store  string `json:"store"`
	Date   string `json:"date"`
	Status string `json:"status"` // not_open | no_data
}

// DuplicateGroup 重复记录分组。
type DuplicateGroup struct {
	Store        string `json:"store"`
	Date         string `json:"date"`
	Count        int    `json:"count"`
	RevenueCents int64  `json:"revenueCents"`
}

// DataQualityReport 数据质量说明。
type DataQualityReport struct {
	Duplicates struct {
		Count        int              `json:"count"`
		RevenueCents int64            `json:"revenueCents"`
		Groups       []DuplicateGroup `json:"groups"`
	} `json:"duplicates"`
	IDConflicts struct {
		Count     int      `json:"count"`
		RecordIDs []string `json:"recordIds"`
	} `json:"idConflicts"`
	AmountMismatch struct {
		Count int `json:"count"`
	} `json:"amountMismatch"`
	UnknownCodes struct {
		Count        int   `json:"count"`
		RevenueCents int64 `json:"revenueCents"`
	} `json:"unknownCodes"`
	Returns struct {
		Count        int           `json:"count"`
		RevenueCents int64         `json:"revenueCents"`
		Groups       []ReturnGroup `json:"groups"`
	} `json:"returns"`
	StoreDays         []StoreDay `json:"storeDays"`
	PrevWeekStoreDays []StoreDay `json:"prevWeekStoreDays"`
}

// TrendPoint 近 4 周实收趋势的一个点。
type TrendPoint struct {
	Week         IsoWeek          `json:"week"`
	CompanyCents int64            `json:"companyCents"`
	Regions      map[string]int64 `json:"regions"`
}

// TopProduct 实收前几名的单品。
type TopProduct struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	RevenueCents int64  `json:"revenueCents"`
}

// WeeklyFacts 场景 A 的全部事实。
type WeeklyFacts struct {
	Period           PeriodRange                                `json:"period"`
	DataCoverage     DateRange                                  `json:"dataCoverage"`
	Company          Metric                                     `json:"company"`
	Regions          map[string]Metric                          `json:"regions"`
	Stores           []StoreMetric                              `json:"stores"`
	Categories       []CategoryMetric                           `json:"categories"`
	RegionCategories map[string]map[string]RegionCategoryMetric `json:"regionCategories"`
	TopProducts      []TopProduct                               `json:"topProducts"`
	Anomalies        []Anomaly                                  `json:"anomalies"`
	Trend            []TrendPoint                               `json:"trend"`
	Quality          DataQualityReport                          `json:"quality"`
}

// SlotFormat 槽位渲染格式，由字段名后缀决定。
type SlotFormat string

const (
	SlotYuan     SlotFormat = "yuan"
	SlotPercent1 SlotFormat = "percent1"
	SlotCount    SlotFormat = "count"
)

// SlotFormats 字段名 → 渲染格式。
var SlotFormats = map[string]SlotFormat{
	"revenue":             SlotYuan,
	"revenueCents":        SlotYuan,
	"prevRevenueCents":    SlotYuan,
	"targetCents":         SlotYuan,
	"wow":                 SlotPercent1,
	"grossMarginRate":     SlotPercent1,
	"prevGrossMarginRate": SlotPercent1,
	"achievement":         SlotPercent1,
	"deliveryShare":       SlotPercent1,
	"count":               SlotCount,
}
