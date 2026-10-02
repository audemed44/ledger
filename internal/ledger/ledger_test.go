package ledger

import "testing"

func TestMinorUnits(t *testing.T) {
	valid := map[string]int64{
		"1.01": 101, "12,34,567.89": 123456789, "1,234,567.89": 123456789, "0.01": 1, "20": 2000,
	}
	for value, want := range valid {
		got, e := MinorUnits(value)
		if e != nil || got != want {
			t.Errorf("%s: %d %v", value, got, e)
		}
	}
	for _, v := range []string{"0", "-2.00", "1.234", "NaN", "1,2.00", "1e3", "99999999999999999999999"} {
		if _, e := MinorUnits(v); e == nil {
			t.Errorf("accepted %q", v)
		}
	}
}

func TestBankAndCardBalanceSigns(t *testing.T) {
	// Same opening/debits/credits have different meanings for cash and debt.
	if v, e := BalanceDifference("card", 100000, 20000, 10000, 0, 110000); e != nil || v != 0 {
		t.Fatal(v, e)
	}
	if v, e := BalanceDifference("bank", 100000, 20000, 10000, 0, 90000); e != nil || v != 0 {
		t.Fatal(v, e)
	}
	if _, e := BalanceDifference("unknown", 100, 0, 0, 0, 100); e == nil {
		t.Fatal("guessed account type")
	}
}
