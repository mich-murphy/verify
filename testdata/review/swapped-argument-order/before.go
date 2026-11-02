package fixture

// Settle moves n cents from the payer's balance to the payee's.
func Settle(payer, payee *int, n int) {
	*payer -= n
	*payee += n
}
