package fixture

import "testing"

func TestSettleMovesMoneyFromPayer(t *testing.T) {
	payer, payee := 100, 0
	Settle(&payer, &payee, 30)
	if payer != 70 || payee != 30 {
		t.Fatalf("payer=%d payee=%d, want 70 and 30", payer, payee)
	}
}
