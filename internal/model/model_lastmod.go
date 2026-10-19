package model

import "time"

// ModelsLastModified is when each model's public page last changed in a way a
// reader would notice: its catalog row was edited, one of its sell prices was
// written, or a discount that covers it was. Discounts with no model name
// cover every model. The public sitemap reports these as <lastmod>, so health
// and traffic figures, which move all the time, are deliberately left out.
func ModelsLastModified() (map[string]time.Time, error) {
	result := map[string]time.Time{}
	bump := func(name string, at time.Time) {
		if at.IsZero() {
			return
		}
		if at.After(result[name]) {
			result[name] = at.UTC()
		}
	}

	var rows []struct {
		ModelName   string
		UpdatedTime int64
	}
	if err := CoreDB().Model(&Model{}).Select("model_name, updated_time").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.UpdatedTime > 0 {
			bump(row.ModelName, time.Unix(row.UpdatedTime, 0))
		}
	}

	// Every sell-price row counts, active or not: a row going inactive is
	// the moment the price on the page changed.
	var prices []struct {
		ModelName string
		UpdatedAt time.Time
	}
	if err := CoreDB().Model(&ModelSellPrice{}).Select("model_name, updated_at").Find(&prices).Error; err != nil {
		return nil, err
	}
	for _, price := range prices {
		bump(price.ModelName, price.UpdatedAt)
	}

	return result, nil
}
