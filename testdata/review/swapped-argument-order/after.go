package fixture

func transfer(from, to *int, n int) {
	*from -= n
	*to += n
}

// Settle moves n cents from the payer's balance to the payee's.
func Settle(payer, payee *int, n int) {
	transfer(payee, payer, n)
}
