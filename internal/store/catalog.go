package store

import (
	_ "embed"
	"encoding/json"
	"sync"
	"time"
)

//go:embed model_prices.json
var modelPriceCatalog []byte

// Prices is the price catalog that ships in the binary (NOTICE): every
// command prices with the catalog of the binary that runs it. The slice is
// shared: callers must not modify it.
func Prices() []ModelPrice { return catalog() }

var catalog = sync.OnceValue(func() []ModelPrice {
	var rows []struct {
		ModelPattern              string    `json:"model_pattern"`
		InputPricePerToken        float64   `json:"input_price_per_token"`
		OutputPricePerToken       float64   `json:"output_price_per_token"`
		CacheReadPricePerToken    *float64  `json:"cache_read_price_per_token"`
		CacheWritePricePerToken   *float64  `json:"cache_write_price_per_token"`
		CacheWrite1hPricePerToken *float64  `json:"cache_write_1h_price_per_token"`
		UpdatedAt                 time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(modelPriceCatalog, &rows); err != nil {
		panic("store: decoding the embedded price catalog: " + err.Error())
	}
	prices := make([]ModelPrice, len(rows))
	for i, r := range rows {
		prices[i] = ModelPrice(r)
	}
	return prices
})
