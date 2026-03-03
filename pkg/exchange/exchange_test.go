package exchange

import (
	"math/big"
	"testing"
)

func TestParseDecimalRat(t *testing.T) {
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

func TestSpendCapComparison(t *testing.T) {
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
