package usage

import "testing"

func TestPriceKnownZeroUnknownRoundingAndCurrency(t *testing.T) {
	for _, test := range []struct {
		name                string
		p                   Price
		input, output, want int64
		known, invalid      bool
	}{
		{"known", Price{Known: true, Currency: "CNY", Input: 2000000, Output: 8000000}, 1000, 100, 2800, true, false},
		{"round up", Price{Known: true, Currency: "USD", Input: 1}, 1, 0, 1, true, false},
		{"explicitly free", Price{Known: true, Currency: "CNY"}, 100, 10, 0, true, false},
		{"unknown is not free", Price{}, 100, 10, 0, false, false},
		{"invalid currency", Price{Known: true, Currency: "EUR"}, 100, 10, 0, false, true},
		{"negative tokens", Price{}, -1, 10, 0, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cost, known, err := test.p.Cost(test.input, test.output)
			if (err != nil) != test.invalid || cost != test.want || known != test.known {
				t.Fatal(cost, known, err)
			}
		})
	}
}
