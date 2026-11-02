package fixture

import (
	"errors"
	"testing"
)

func TestWithdrawRefusesOverdraft(t *testing.T) {
	if got, err := Withdraw(10, 4); err != nil || got != 6 {
		t.Fatalf("Withdraw(10, 4) = %d, %v", got, err)
	}
	if got, err := Withdraw(3, 5); !errors.Is(err, errInsufficient) || got != 3 {
		t.Fatalf("Withdraw(3, 5) = %d, %v; want 3 and errInsufficient", got, err)
	}
}
