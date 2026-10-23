// Package usage separates provider-reported usage, estimates and unknown costs.
package usage

import (
	"agent-workbench/internal/catalog"
	"errors"
)

// Price is a persisted snapshot in micro currency units per million tokens.
type Price struct {
	Known    bool   `json:"known"`
	Currency string `json:"currency,omitempty"`
	Input    int64  `json:"input_micros_per_million"`
	Output   int64  `json:"output_micros_per_million"`
}

// Snapshot freezes a manually configured price, without querying or guessing provider pricing.
func Snapshot(m catalog.Model) Price {
	return Price{Known: m.PriceKnown, Currency: m.Currency, Input: m.InputPrice, Output: m.OutputPrice}
}

// Cost rounds up to a micro unit. Unknown prices are never returned as a known zero.
func (p Price) Cost(input, output int64) (int64, bool, error) {
	if input < 0 || output < 0 || input > 1000000000 || output > 1000000000 || p.Input < 0 || p.Output < 0 || p.Input > 1000000000 || p.Output > 1000000000 {
		return 0, false, errors.New("用量或价格超过有效范围")
	}
	if !p.Known {
		return 0, false, nil
	}
	if p.Currency != "USD" && p.Currency != "CNY" {
		return 0, false, errors.New("价格币种无效")
	}
	return (input*p.Input + output*p.Output + 999999) / 1000000, true, nil
}
