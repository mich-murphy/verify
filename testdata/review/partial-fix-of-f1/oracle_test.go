package fixture

import "testing"

func TestPercentClampsBothEnds(t *testing.T) {
	if Percent(-5) != 0 || Percent(150) != 100 || Percent(40) != 40 {
		t.Fatalf("Percent(-5)=%d Percent(150)=%d Percent(40)=%d", Percent(-5), Percent(150), Percent(40))
	}
}
