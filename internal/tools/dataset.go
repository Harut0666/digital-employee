package tools

// 清洗后的数据集（移植自 packages/tools/src/sales/dataset.ts）：
// load_sales 产出的 JSON 文件格式，以及读写它的函数。
//
// 清洗规则（design.md §10.2）：
// - 记录ID 重复：只保留第一条，其余记入 removed；内容与首条不一致的标为 conflict
// - 实收 ≠ 原价 − 折扣：保留，以实收为准，标 mismatch（compute_metrics 按周报告条数）
// - 门店或商品编码在维表里查不到：保留但标 unknown，不计入任何合计

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"digital-employee/internal/contract"
)

const datasetVersion = 1

// SaleRow 数据集里的一行（金额单位：分）。
type SaleRow struct {
	ID            string `json:"id"`
	Date          string `json:"date"`
	Store         string `json:"store"`
	Channel       string `json:"channel"`
	Product       string `json:"product"`
	Qty           int    `json:"qty"`
	GrossCents    int64  `json:"grossCents"`
	DiscountCents int64  `json:"discountCents"`
	NetCents      int64  `json:"netCents"`
	CostCents     int64  `json:"costCents"`
	// Unknown 门店或商品编码查不到
	Unknown bool `json:"unknown,omitempty"`
	// Mismatch 实收 ≠ 原价 − 折扣
	Mismatch bool `json:"mismatch,omitempty"`
}

// RemovedRow 被剔除的重复记录。
type RemovedRow struct {
	ID       string `json:"id"`
	Store    string `json:"store"`
	Date     string `json:"date"`
	NetCents int64  `json:"netCents"`
	// Conflict true：与首条内容不一致（ID 冲突）；false：完全重复
	Conflict bool `json:"conflict"`
}

// DatasetSource 数据集来源。
type DatasetSource struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

// Dataset 清洗后的数据集。
type Dataset struct {
	Version  int                `json:"version"`
	Source   DatasetSource      `json:"source"`
	RawRows  int                `json:"rawRows"`
	Coverage contract.DateRange `json:"coverage"`
	Rows     []SaleRow          `json:"rows"`
	Removed  []RemovedRow       `json:"removed"`
	Stores   []StoreInfo        `json:"stores"`
	Products []ProductInfo      `json:"products"`
	Targets  []TargetInfo       `json:"targets"`
}

func signature(r RawSale) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d|%d|%d|%d|%d",
		r.Date, r.Store, r.Channel, r.Product, r.Qty, r.GrossCents, r.DiscountCents, r.NetCents, r.CostCents)
}

// cleanSales 去重并标记异常行。
func cleanSales(raw []RawSale, stores []StoreInfo, products []ProductInfo) ([]SaleRow, []RemovedRow) {
	var storeSet, productSet map[string]struct{}
	if stores != nil {
		storeSet = make(map[string]struct{}, len(stores))
		for _, s := range stores {
			storeSet[s.Code] = struct{}{}
		}
	}
	if products != nil {
		productSet = make(map[string]struct{}, len(products))
		for _, p := range products {
			productSet[p.Code] = struct{}{}
		}
	}
	first := make(map[string]string, len(raw))
	rows := make([]SaleRow, 0, len(raw))
	removed := make([]RemovedRow, 0)
	for _, r := range raw {
		if seen, ok := first[r.ID]; ok {
			removed = append(removed, RemovedRow{
				ID:       r.ID,
				Store:    r.Store,
				Date:     r.Date,
				NetCents: r.NetCents,
				Conflict: seen != signature(r),
			})
			continue
		}
		first[r.ID] = signature(r)
		row := SaleRow{
			ID:            r.ID,
			Date:          r.Date,
			Store:         r.Store,
			Channel:       r.Channel,
			Product:       r.Product,
			Qty:           r.Qty,
			GrossCents:    r.GrossCents,
			DiscountCents: r.DiscountCents,
			NetCents:      r.NetCents,
			CostCents:     r.CostCents,
		}
		if storeSet != nil {
			if _, ok := storeSet[r.Store]; !ok {
				row.Unknown = true
			}
		}
		if productSet != nil {
			if _, ok := productSet[r.Product]; !ok {
				row.Unknown = true
			}
		}
		if r.NetCents != r.GrossCents-r.DiscountCents {
			row.Mismatch = true
		}
		rows = append(rows, row)
	}
	return rows, removed
}

// datasetPathFor 数据集文件名取内容哈希。
func datasetPathFor(outDir, digest string) string {
	return filepath.Join(outDir, fmt.Sprintf("sales-%s-v%d.json", digest[:32], datasetVersion))
}

// LoadSales 读取并清洗销售数据，写成内容哈希命名的 JSON；文件已存在则直接复用。
func LoadSales(salesPath, outDir string) (contract.LoadSalesOutput, error) {
	bytes, err := os.ReadFile(salesPath)
	if err != nil {
		return contract.LoadSalesOutput{}, err
	}
	sum := sha256.Sum256(bytes)
	digest := fmt.Sprintf("%x", sum)
	datasetPath := datasetPathFor(outDir, digest)

	if _, err := os.Stat(datasetPath); err == nil {
		ds, err := readDataset(datasetPath)
		if err != nil {
			return contract.LoadSalesOutput{}, err
		}
		return contract.LoadSalesOutput{DatasetPath: datasetPath, Rows: ds.RawRows, Coverage: ds.Coverage}, nil
	}

	src, err := readSalesSource(salesPath)
	if err != nil {
		return contract.LoadSalesOutput{}, err
	}
	coverage := coverageOf(src.Sales)
	if coverage == nil {
		return contract.LoadSalesOutput{}, fmt.Errorf("销售明细中没有可识别的销售日期")
	}
	rows, removed := cleanSales(src.Sales, src.Stores, src.Products)
	ds := Dataset{
		Version:  datasetVersion,
		Source:   DatasetSource{Name: filepath.Base(salesPath), Kind: src.Kind, SHA256: digest},
		RawRows:  len(src.Sales),
		Coverage: *coverage,
		Rows:     rows,
		Removed:  removed,
		Stores:   src.Stores,
		Products: src.Products,
		Targets:  src.Targets,
	}
	payload, err := json.Marshal(ds)
	if err != nil {
		return contract.LoadSalesOutput{}, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return contract.LoadSalesOutput{}, err
	}
	tmp, err := os.CreateTemp(outDir, filepath.Base(datasetPath)+".*.tmp")
	if err != nil {
		return contract.LoadSalesOutput{}, err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return contract.LoadSalesOutput{}, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return contract.LoadSalesOutput{}, err
	}
	if err := os.Rename(tmpName, datasetPath); err != nil {
		os.Remove(tmpName)
		return contract.LoadSalesOutput{}, err
	}
	return contract.LoadSalesOutput{DatasetPath: datasetPath, Rows: ds.RawRows, Coverage: ds.Coverage}, nil
}

// readDataset 读回数据集文件（做格式校验）。
func readDataset(path string) (*Dataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ds Dataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("数据集文件格式不正确：%s", path)
	}
	if ds.Version != datasetVersion || ds.Rows == nil {
		return nil, fmt.Errorf("数据集文件格式不正确：%s", path)
	}
	return &ds, nil
}
