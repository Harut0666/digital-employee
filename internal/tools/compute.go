package tools

// compute_metrics：按《周报格式要求》口径从清洗后的数据集算出 WeeklyFacts
// （移植自 packages/tools/src/compute.ts）。金额全程为整数“分”。
//
// 数组顺序是下游（render/QC）的隐式契约：
// - Stores 按门店维表原始顺序；Categories 按品类维表首次出现顺序；
// - TopProducts 按实收降序（同额按编码升序）；
// - Anomalies 先区域（regionOrder）、后门店（门店维表序）；
// - Trend 按周升序。

import (
	"fmt"
	"math"
	"sort"

	"digital-employee/internal/contract"
)

type totals struct {
	rev      int64
	cost     int64
	delivery int64
	hasData  bool
}

func zeroTotals() *totals { return &totals{} }

type weekAgg struct {
	byStore    map[string]*totals
	byProduct  map[string]int64
	storeDates map[string]struct{}
	hasData    bool
}

// aggregate 汇总 [from, to] 的有效记录（已去重；编码查不到的不计入任何合计）。
func aggregate(ds *Dataset, from, to string) *weekAgg {
	agg := &weekAgg{
		byStore:    make(map[string]*totals, len(ds.Stores)),
		byProduct:  make(map[string]int64, len(ds.Products)),
		storeDates: make(map[string]struct{}),
	}
	for i := range ds.Rows {
		r := &ds.Rows[i]
		if r.Unknown || r.Date < from || r.Date > to {
			continue
		}
		t, ok := agg.byStore[r.Store]
		if !ok {
			t = zeroTotals()
			agg.byStore[r.Store] = t
		}
		t.rev += r.NetCents
		t.cost += r.CostCents
		if r.Channel == "外卖" {
			t.delivery += r.NetCents
		}
		t.hasData = true
		agg.byProduct[r.Product] += r.NetCents
		agg.storeDates[r.Store+"|"+r.Date] = struct{}{}
		agg.hasData = true
	}
	return agg
}

func sumTotals(list []*totals) totals {
	t := totals{}
	for _, x := range list {
		if x == nil {
			continue
		}
		t.rev += x.rev
		t.cost += x.cost
		t.delivery += x.delivery
		t.hasData = t.hasData || x.hasData
	}
	return t
}

func metricOf(cur, prev totals, prevExists bool, targetCents *int64) contract.Metric {
	m := contract.Metric{RevenueCents: cur.rev}
	if prevExists {
		p := prev.rev
		m.PrevRevenueCents = &p
	}
	if m.PrevRevenueCents != nil && *m.PrevRevenueCents != 0 {
		wow := float64(cur.rev-*m.PrevRevenueCents) / float64(*m.PrevRevenueCents)
		m.Wow = &wow
	}
	hasRev := cur.rev > 0
	if hasRev {
		gm := float64(cur.rev-cur.cost) / float64(cur.rev)
		m.GrossMarginRate = &gm
	}
	m.TargetCents = targetCents
	if targetCents != nil && *targetCents != 0 {
		ach := float64(cur.rev) / float64(*targetCents)
		m.Achievement = &ach
	}
	if hasRev {
		share := float64(cur.delivery) / float64(cur.rev)
		m.DeliveryShare = &share
	}
	return m
}

// regionCategoryTotals 区域×品类的实收与成本：门店编码经门店维表映射到区域，
// 商品编码经商品维表映射到品类。编码查不到的记录已在清洗时标记 unknown，不计入。
func regionCategoryTotals(ds *Dataset, from, to string) map[string]map[string]*totals {
	regionOf := make(map[string]string, len(ds.Stores))
	for _, s := range ds.Stores {
		regionOf[s.Code] = s.Region
	}
	categoryOf := make(map[string]string, len(ds.Products))
	for _, p := range ds.Products {
		categoryOf[p.Code] = p.Category
	}
	out := make(map[string]map[string]*totals)
	for i := range ds.Rows {
		r := &ds.Rows[i]
		if r.Unknown || r.Date < from || r.Date > to {
			continue
		}
		region, ok := regionOf[r.Store]
		if !ok {
			continue
		}
		category, ok := categoryOf[r.Product]
		if !ok {
			continue
		}
		byCategory, ok := out[region]
		if !ok {
			byCategory = make(map[string]*totals)
			out[region] = byCategory
		}
		t, ok := byCategory[category]
		if !ok {
			t = zeroTotals()
			byCategory[category] = t
		}
		t.rev += r.NetCents
		t.cost += r.CostCents
		t.hasData = true
	}
	return out
}

// storeDaysOf 未营业/无数据的门店日。只判断数据覆盖范围内的日期。
func storeDaysOf(ds *Dataset, agg *weekAgg, from, to string) []contract.StoreDay {
	lo := from
	if ds.Coverage.From > lo {
		lo = ds.Coverage.From
	}
	hi := to
	if ds.Coverage.To < hi {
		hi = ds.Coverage.To
	}
	out := make([]contract.StoreDay, 0)
	if lo > hi {
		return out
	}
	dates := datesBetween(lo, hi)
	for _, s := range ds.Stores {
		for _, d := range dates {
			if _, ok := agg.storeDates[s.Code+"|"+d]; ok {
				continue
			}
			status := "no_data"
			if d < s.OpenDate {
				status = "not_open"
			}
			out = append(out, contract.StoreDay{Store: s.Code, Date: d, Status: status})
		}
	}
	return out
}

// ComputeMetrics 计算 WeeklyFacts。
func ComputeMetrics(input contract.ComputeMetricsInput) (contract.WeeklyFacts, error) {
	var result contract.WeeklyFacts
	ds, err := readDataset(input.DatasetPath)
	if err != nil {
		return result, err
	}
	if len(ds.Stores) == 0 || len(ds.Products) == 0 || len(ds.Targets) == 0 {
		return result, fmt.Errorf("数据集缺少门店信息、商品信息或销售目标维表，无法按区域、品类和目标统计（只有 CSV 的数据集不能用于周报）")
	}
	period := PeriodRangeOf(input.Period)
	if period.To < ds.Coverage.From || period.From > ds.Coverage.To {
		return result, fmt.Errorf("统计周期 %s 至 %s 超出数据覆盖范围（%s 至 %s）",
			period.From, period.To, ds.Coverage.From, ds.Coverage.To)
	}
	prevWeek := ShiftWeek(input.Period, -1)
	prevRange := PeriodRangeOf(prevWeek)

	cur := aggregate(ds, period.From, period.To)
	prev := aggregate(ds, prevRange.From, prevRange.To)
	prevExists := prev.hasData

	// 区域顺序 = 门店维表里区域首次出现的顺序
	regionOrder := make([]string, 0, len(ds.Stores))
	seenRegion := make(map[string]struct{}, len(ds.Stores))
	for _, s := range ds.Stores {
		if _, ok := seenRegion[s.Region]; ok {
			continue
		}
		seenRegion[s.Region] = struct{}{}
		regionOrder = append(regionOrder, s.Region)
	}
	storesOfRegion := func(region string) []StoreInfo {
		list := make([]StoreInfo, 0, len(ds.Stores))
		for _, s := range ds.Stores {
			if s.Region == region {
				list = append(list, s)
			}
		}
		return list
	}
	targetOf := func(region string, w contract.IsoWeek) *int64 {
		for i := range ds.Targets {
			t := &ds.Targets[i]
			if t.Region == region && t.Year == w.Year && t.Week == w.Week {
				v := t.TargetCents
				return &v
			}
		}
		return nil
	}
	regionTotals := func(agg *weekAgg, region string) totals {
		stores := storesOfRegion(region)
		list := make([]*totals, 0, len(stores))
		for _, s := range stores {
			list = append(list, agg.byStore[s.Code])
		}
		return sumTotals(list)
	}

	// 区域与全公司
	regions := make(map[string]contract.Metric, len(regionOrder))
	regionTargets := make([]*int64, 0, len(regionOrder))
	for _, region := range regionOrder {
		target := targetOf(region, input.Period)
		regionTargets = append(regionTargets, target)
		regions[region] = metricOf(regionTotals(cur, region), regionTotals(prev, region), prevExists, target)
	}
	allTargetsPresent := true
	var companyTargetSum int64
	for _, t := range regionTargets {
		if t == nil {
			allTargetsPresent = false
			break
		}
		companyTargetSum += *t
	}
	var companyTarget *int64
	if allTargetsPresent {
		companyTarget = &companyTargetSum
	}
	sumAll := func(agg *weekAgg) totals {
		list := make([]*totals, 0, len(ds.Stores))
		for _, s := range ds.Stores {
			list = append(list, agg.byStore[s.Code])
		}
		return sumTotals(list)
	}
	company := metricOf(sumAll(cur), sumAll(prev), prevExists, companyTarget)

	// 门店
	stores := make([]contract.StoreMetric, 0, len(ds.Stores))
	for _, s := range ds.Stores {
		c := cur.byStore[s.Code]
		if c == nil {
			c = zeroTotals()
		}
		p := prev.byStore[s.Code]
		if p == nil {
			p = zeroTotals()
		}
		sm := contract.StoreMetric{
			Metric: metricOf(*c, *p, p.hasData, nil),
			Code:   s.Code,
			Name:   s.Name,
			Region: s.Region,
			Type:   contract.StoreType(s.StoreType),
		}
		stores = append(stores, sm)
	}

	// 品类与单品
	categoryOf := make(map[string]string, len(ds.Products))
	for _, p := range ds.Products {
		categoryOf[p.Code] = p.Category
	}
	categoryRev := func(agg *weekAgg) map[string]int64 {
		m := make(map[string]int64)
		for code, rev := range agg.byProduct {
			c, ok := categoryOf[code]
			if !ok {
				continue
			}
			m[c] += rev
		}
		return m
	}
	curCat := categoryRev(cur)
	prevCat := categoryRev(prev)
	categories := make([]contract.CategoryMetric, 0, len(ds.Products))
	categoryOrder := make([]string, 0, len(ds.Products))
	seenCategory := make(map[string]struct{}, len(ds.Products))
	for _, p := range ds.Products {
		if _, ok := seenCategory[p.Category]; ok {
			continue
		}
		seenCategory[p.Category] = struct{}{}
		categoryOrder = append(categoryOrder, p.Category)
		revenueCents := curCat[p.Category]
		var prevRevenueCents *int64
		if prevExists {
			v := prevCat[p.Category]
			prevRevenueCents = &v
		}
		var wow *float64
		if prevRevenueCents != nil && *prevRevenueCents != 0 {
			w := float64(revenueCents-*prevRevenueCents) / float64(*prevRevenueCents)
			wow = &w
		}
		categories = append(categories, contract.CategoryMetric{
			Category:         p.Category,
			RevenueCents:     revenueCents,
			PrevRevenueCents: prevRevenueCents,
			Wow:              wow,
		})
	}
	nameOf := make(map[string]string, len(ds.Products))
	for _, p := range ds.Products {
		nameOf[p.Code] = p.Name
	}
	type productRevenue struct {
		code string
		rev  int64
	}
	ranked := make([]productRevenue, 0, len(cur.byProduct))
	for code, rev := range cur.byProduct {
		if _, ok := nameOf[code]; !ok {
			continue
		}
		ranked = append(ranked, productRevenue{code: code, rev: rev})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].rev != ranked[j].rev {
			return ranked[i].rev > ranked[j].rev
		}
		return ranked[i].code < ranked[j].code
	})
	if len(ranked) > 5 {
		ranked = ranked[:5]
	}
	topProducts := make([]contract.TopProduct, 0, len(ranked))
	for _, p := range ranked {
		topProducts = append(topProducts, contract.TopProduct{Code: p.code, Name: nameOf[p.code], RevenueCents: p.rev})
	}

	// 区域×品类：区域与品类的范围取自维表，没有数据也保留（值为 0 / null），槽位始终能解析。
	curRegionCategory := regionCategoryTotals(ds, period.From, period.To)
	prevRegionCategory := regionCategoryTotals(ds, prevRange.From, prevRange.To)
	regionCategories := make(map[string]map[string]contract.RegionCategoryMetric, len(regionOrder))
	for _, region := range regionOrder {
		row := make(map[string]contract.RegionCategoryMetric, len(categoryOrder))
		for _, category := range categoryOrder {
			var c, p *totals
			if m, ok := curRegionCategory[region]; ok {
				c = m[category]
			}
			if m, ok := prevRegionCategory[region]; ok {
				p = m[category]
			}
			var revenueCents, prevRevenueCents int64
			if c != nil {
				revenueCents = c.rev
			}
			if p != nil {
				prevRevenueCents = p.rev
			}
			rc := contract.RegionCategoryMetric{RevenueCents: revenueCents}
			if prevExists {
				v := prevRevenueCents
				rc.PrevRevenueCents = &v
			}
			if c != nil && revenueCents > 0 {
				gm := float64(c.rev-c.cost) / float64(c.rev)
				rc.GrossMarginRate = &gm
			}
			if prevExists && p != nil && prevRevenueCents > 0 {
				pgm := float64(p.rev-p.cost) / float64(p.rev)
				rc.PrevGrossMarginRate = &pgm
			}
			row[category] = rc
		}
		regionCategories[region] = row
	}

	// 数据质量
	inPeriod := func(d string) bool { return d >= period.From && d <= period.To }
	removedIn := make([]RemovedRow, 0)
	for _, r := range ds.Removed {
		if inPeriod(r.Date) {
			removedIn = append(removedIn, r)
		}
	}
	dups := make([]RemovedRow, 0, len(removedIn))
	for _, r := range removedIn {
		if !r.Conflict {
			dups = append(dups, r)
		}
	}
	groupMap := make(map[string]*contract.DuplicateGroup)
	groupOrder := make([]string, 0)
	for _, r := range dups {
		k := r.Date + "|" + r.Store
		g, ok := groupMap[k]
		if !ok {
			g = &contract.DuplicateGroup{Store: r.Store, Date: r.Date}
			groupMap[k] = g
			groupOrder = append(groupOrder, k)
		}
		g.Count++
		g.RevenueCents += r.NetCents
	}
	duplicateGroups := make([]contract.DuplicateGroup, 0, len(groupOrder))
	var duplicateRevenue int64
	for _, k := range groupOrder {
		g := groupMap[k]
		duplicateRevenue += g.RevenueCents
		duplicateGroups = append(duplicateGroups, *g)
	}
	sort.Slice(duplicateGroups, func(i, j int) bool {
		if duplicateGroups[i].Date != duplicateGroups[j].Date {
			return duplicateGroups[i].Date < duplicateGroups[j].Date
		}
		return duplicateGroups[i].Store < duplicateGroups[j].Store
	})

	keptIn := make([]SaleRow, 0, len(ds.Rows))
	for _, r := range ds.Rows {
		if inPeriod(r.Date) {
			keptIn = append(keptIn, r)
		}
	}
	unknown := make([]SaleRow, 0)
	for _, r := range keptIn {
		if r.Unknown {
			unknown = append(unknown, r)
		}
	}
	returns := make([]SaleRow, 0)
	for _, r := range keptIn {
		if !r.Unknown && r.Qty < 0 {
			returns = append(returns, r)
		}
	}
	// 退货明细：同一门店、同一日期、同一单品合并成一组，按日期、门店、商品排序
	storeNameOf := make(map[string]string, len(ds.Stores))
	for _, s := range ds.Stores {
		storeNameOf[s.Code] = s.Name
	}
	returnGroups := make(map[string]*contract.ReturnGroup)
	returnOrder := make([]string, 0)
	for _, r := range returns {
		k := r.Store + "|" + r.Date + "|" + r.Product
		g, ok := returnGroups[k]
		if !ok {
			name := storeNameOf[r.Store]
			if name == "" {
				name = r.Store
			}
			productName := nameOf[r.Product]
			if productName == "" {
				productName = r.Product
			}
			g = &contract.ReturnGroup{
				Store:       r.Store,
				StoreName:   name,
				Date:        r.Date,
				Product:     r.Product,
				ProductName: productName,
			}
			returnGroups[k] = g
			returnOrder = append(returnOrder, k)
		}
		g.Count += -r.Qty
		g.RevenueCents += r.NetCents
	}
	returnList := make([]contract.ReturnGroup, 0, len(returnOrder))
	var returnRevenue int64
	for _, k := range returnOrder {
		g := returnGroups[k]
		returnRevenue += g.RevenueCents
		returnList = append(returnList, *g)
	}
	sort.Slice(returnList, func(i, j int) bool {
		if returnList[i].Date != returnList[j].Date {
			return returnList[i].Date < returnList[j].Date
		}
		if returnList[i].Store != returnList[j].Store {
			return returnList[i].Store < returnList[j].Store
		}
		return returnList[i].Product < returnList[j].Product
	})

	conflictIDs := make([]string, 0)
	seenConflict := make(map[string]struct{})
	for _, r := range removedIn {
		if !r.Conflict {
			continue
		}
		if _, ok := seenConflict[r.ID]; ok {
			continue
		}
		seenConflict[r.ID] = struct{}{}
		conflictIDs = append(conflictIDs, r.ID)
	}
	mismatchCount := 0
	for _, r := range keptIn {
		if r.Mismatch {
			mismatchCount++
		}
	}
	var unknownRevenue int64
	for _, r := range unknown {
		unknownRevenue += r.NetCents
	}
	curDays := storeDaysOf(ds, cur, period.From, period.To)
	prevDays := storeDaysOf(ds, prev, prevRange.From, prevRange.To)
	quality := contract.DataQualityReport{}
	quality.Duplicates.Count = len(dups)
	quality.Duplicates.RevenueCents = duplicateRevenue
	quality.Duplicates.Groups = duplicateGroups
	quality.IDConflicts.Count = len(conflictIDs)
	quality.IDConflicts.RecordIDs = conflictIDs
	quality.AmountMismatch.Count = mismatchCount
	quality.UnknownCodes.Count = len(unknown)
	quality.UnknownCodes.RevenueCents = unknownRevenue
	quality.Returns.Count = len(returns)
	quality.Returns.RevenueCents = returnRevenue
	quality.Returns.Groups = returnList
	quality.StoreDays = curDays
	quality.PrevWeekStoreDays = prevDays

	// 异常：用未取整的环比，严格大于阈值
	missingPrev := func(pred func(store string) bool) bool {
		for _, d := range prevDays {
			if d.Status == "no_data" && pred(d.Store) {
				return true
			}
		}
		return false
	}
	regionOfStore := make(map[string]string, len(ds.Stores))
	for _, s := range ds.Stores {
		regionOfStore[s.Code] = s.Region
	}
	anomalies := make([]contract.Anomaly, 0)
	for _, region := range regionOrder {
		m := regions[region]
		if m.Wow == nil || math.Abs(*m.Wow) <= input.AnomalyThreshold {
			continue
		}
		anomalies = append(anomalies, contract.Anomaly{
			ID:                     "region:" + region,
			Level:                  "region",
			Key:                    region,
			Name:                   region,
			Region:                 region,
			Wow:                    *m.Wow,
			RevenueCents:           m.RevenueCents,
			PrevRevenueCents:       *m.PrevRevenueCents,
			BaseWeekHasMissingDays: missingPrev(func(store string) bool { return regionOfStore[store] == region }),
		})
	}
	for _, s := range stores {
		if s.Wow == nil || math.Abs(*s.Wow) <= input.AnomalyThreshold {
			continue
		}
		anomalies = append(anomalies, contract.Anomaly{
			ID:                     "store:" + s.Code,
			Level:                  "store",
			Key:                    s.Code,
			Name:                   s.Name,
			Region:                 s.Region,
			StoreType:              s.Type,
			Wow:                    *s.Wow,
			RevenueCents:           s.RevenueCents,
			PrevRevenueCents:       *s.PrevRevenueCents,
			BaseWeekHasMissingDays: missingPrev(func(code string) bool { return code == s.Code }),
		})
	}

	// 近 4 周趋势（含本周；没有数据的周不画）
	trend := make([]contract.TrendPoint, 0, 4)
	for k := 3; k >= 0; k-- {
		week := ShiftWeek(input.Period, -k)
		r := PeriodRangeOf(week)
		var agg *weekAgg
		switch k {
		case 0:
			agg = cur
		case 1:
			agg = prev
		default:
			agg = aggregate(ds, r.From, r.To)
		}
		if !agg.hasData {
			continue
		}
		regionValues := make(map[string]int64, len(regionOrder))
		for _, region := range regionOrder {
			regionValues[region] = regionTotals(agg, region).rev
		}
		trend = append(trend, contract.TrendPoint{
			Week:         week,
			CompanyCents: sumAll(agg).rev,
			Regions:      regionValues,
		})
	}

	result = contract.WeeklyFacts{
		Period:           period,
		DataCoverage:     ds.Coverage,
		Company:          company,
		Regions:          regions,
		Stores:           stores,
		Categories:       categories,
		RegionCategories: regionCategories,
		TopProducts:      topProducts,
		Anomalies:        anomalies,
		Trend:            trend,
		Quality:          quality,
	}
	return result, nil
}
