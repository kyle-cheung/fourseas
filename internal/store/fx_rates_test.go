package store

import (
	"context"
	"testing"

	"github.com/kyle-cheung/fourseas/providence/internal/model"
)

func TestRequiredFXCurrenciesReturnsOldestDate(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	usd := sample()
	usd.ExternalID, usd.Date, usd.Currency = "txn-usd", day("2026-08-01"), "USD"
	firstCAD := sample()
	firstCAD.ExternalID, firstCAD.Date, firstCAD.Currency = "txn-cad-first", day("2026-08-02"), "CAD"
	secondCAD := sample()
	secondCAD.ExternalID, secondCAD.Date, secondCAD.Currency = "txn-cad-second", day("2026-08-03"), "CAD"
	if err := s.Upsert(ctx, []model.Transaction{usd, firstCAD, secondCAD}); err != nil {
		t.Fatalf("upsert transactions: %v", err)
	}

	got, err := s.RequiredFXCurrencies(ctx, model.BaseCurrency)
	if err != nil {
		t.Fatalf("required FX currencies: %v", err)
	}
	want := []model.FXCurrency{{Currency: "CAD", OldestTransactionDate: day("2026-08-02")}}
	if len(got) != len(want) {
		t.Fatalf("required FX currencies = %+v, want %+v", got, want)
	}
	if got[0].Currency != want[0].Currency || !got[0].OldestTransactionDate.Equal(want[0].OldestTransactionDate) {
		t.Errorf("required FX currencies = %+v, want %+v", got, want)
	}
}

func TestUpsertFXRatesNormalizesAndReplacesExactDecimal(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	rate := model.FXRate{
		Date: day("2026-08-02"), Currency: " cad ", BaseCurrency: "usd", Rate: dec("0.71234567"),
	}
	if err := s.UpsertFXRates(ctx, []model.FXRate{rate}); err != nil {
		t.Fatalf("first upsert FX rate: %v", err)
	}
	rate.Rate = dec("0.72345678")
	if err := s.UpsertFXRates(ctx, []model.FXRate{rate}); err != nil {
		t.Fatalf("second upsert FX rate: %v", err)
	}

	var currency, base string
	var rawRate any
	if err := s.db.QueryRowContext(ctx, `SELECT currency, base_currency, rate FROM fx_rates`).Scan(&currency, &base, &rawRate); err != nil {
		t.Fatalf("read FX rate: %v", err)
	}
	rateValue, err := toDecimal(rawRate)
	if err != nil {
		t.Fatalf("read decimal rate: %v", err)
	}
	if currency != "CAD" || base != "USD" || !rateValue.Equal(dec("0.72345678")) {
		t.Errorf("stored FX rate = %s/%s %s, want CAD/USD 0.72345678", currency, base, rateValue)
	}
}
