package sqlite

import (
	"context"
	"fmt"

	"github.com/jhisse/spoor/internal/store"
)

func (s *Store) ListModelPrices(ctx context.Context) ([]store.ModelPrice, error) {
	prices, err := queryAll(ctx, s.db, func(sc scanner) (p store.ModelPrice, err error) {
		return p, sc.Scan(&p.ModelPattern, &p.InputPricePerToken, &p.OutputPricePerToken,
			&p.CacheReadPricePerToken, &p.CacheWritePricePerToken, &p.CacheWrite1hPricePerToken, at{&p.UpdatedAt})
	}, `SELECT model_pattern, input_price_per_token, output_price_per_token, cache_read_price_per_token,
		cache_write_price_per_token, cache_write_1h_price_per_token, updated_at FROM model_prices`)
	if err != nil {
		return nil, fmt.Errorf("querying model prices: %w", err)
	}
	return prices, nil
}

func (s *Store) ListGenerationModels(ctx context.Context) ([]string, error) {
	models, err := queryAll(ctx, s.db, func(sc scanner) (m string, err error) { return m, sc.Scan(&m) },
		`SELECT DISTINCT model FROM spans WHERE kind = ? AND model IS NOT NULL ORDER BY model`,
		string(store.SpanKindGeneration))
	if err != nil {
		return nil, fmt.Errorf("querying generation models: %w", err)
	}
	return models, nil
}
