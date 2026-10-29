package fixture

import "testing"

func TestMemberRequired(t *testing.T) {
	if CanRead(false, true) || !CanRead(true, true) {
		t.Fatal("membership must gate visible documents")
	}
}
