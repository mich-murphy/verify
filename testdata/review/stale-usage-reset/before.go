package fixture

// Usage tracks the active window's credits.
type Usage struct {
	Credits int
	Window  int
}

func (u *Usage) Update(window, credits int) {
	if u.Window != window {
		u.Window = window
		u.Credits = 0
	}
	u.Credits += credits
}
