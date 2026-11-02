package fixture

import "errors"

var errInsufficient = errors.New("insufficient funds")

// Withdraw subtracts amount from balance and refuses to overdraw.
func Withdraw(balance, amount int) (int, error) {
	if amount > balance {
		return balance, errInsufficient
	}
	return balance - amount, nil
}
