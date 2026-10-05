package sqlite

import (
	"database/sql"
	_ "embed"
)

// modelPriceCatalog is the price table that ships in the binary (NOTICE): a
// JSON array whose keys are the columns of model_prices.
//
//go:embed model_prices.json
var modelPriceCatalog string

// syncModelPriceCatalog makes model_prices the embedded catalog in one
// transaction, at every start: a new binary brings its prices.
func syncModelPriceCatalog(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM model_prices`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO model_prices
		SELECT value->>'model_pattern', value->>'input_price_per_token', value->>'output_price_per_token', value->>'cache_read_price_per_token',
		       value->>'cache_write_price_per_token', value->>'cache_write_1h_price_per_token', value->>'updated_at' FROM json_each(?)`, modelPriceCatalog); err != nil {
		return err
	}
	return tx.Commit()
}
