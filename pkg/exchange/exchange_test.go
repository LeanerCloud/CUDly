package exchange

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

func TestParseDecimalRat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"5.00", "5", false},
		{"0.10", "1/10", false},
		{"-1.25", "-5/4", false},
		{"", "", true},
		{"abc", "", true},
	}

	for _, c := range cases {
		got, err := ParseDecimalRat(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("expected error for %q", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", c.in, err)
		}
		if got.RatString() != c.want {
			t.Fatalf("ParseDecimalRat(%q)=%s want %s", c.in, got.RatString(), c.want)
		}
	}
}

func TestPaymentDueUSDStr_InJSON(t *testing.T) {
	t.Parallel()
	s := &ExchangeQuoteSummary{
		IsValidExchange:  true,
		PaymentDueRaw:    "123.456000",
		PaymentDueUSD:    new(big.Rat).SetFrac64(123456, 1000),
		PaymentDueUSDStr: new(big.Rat).SetFrac64(123456, 1000).FloatString(6),
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	jsonStr := string(data)

	// PaymentDueUSDStr should appear in JSON
	if !strings.Contains(jsonStr, `"payment_due_usd"`) {
		t.Fatalf("expected payment_due_usd in JSON, got: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, "123.456000") {
		t.Fatalf("expected 123.456000 in JSON, got: %s", jsonStr)
	}

	// PaymentDueUSD (big.Rat) should NOT appear in JSON (tagged json:"-")
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := m["PaymentDueUSD"]; ok {
		t.Fatalf("PaymentDueUSD should not appear in JSON")
	}
}

func TestSpendCapComparison(t *testing.T) {
	t.Parallel()
	// paymentDue > cap => reject
	payment := new(big.Rat).SetInt64(6)
	cap := new(big.Rat).SetInt64(5)

	if payment.Cmp(cap) != 1 {
		t.Fatalf("expected payment > cap")
	}

	// equal => ok
	payment2 := new(big.Rat).SetInt64(5)
	if payment2.Cmp(cap) != 0 {
		t.Fatalf("expected payment == cap")
	}

	// less => ok
	payment3 := new(big.Rat).SetInt64(4)
	if payment3.Cmp(cap) != -1 {
		t.Fatalf("expected payment < cap")
	}
}
