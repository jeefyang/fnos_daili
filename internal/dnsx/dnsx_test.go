package dnsx

import "testing"

func TestValidate(t *testing.T) {
	valid := []string{"", "223.5.5.5", " 1.1.1.1 ", "2400:3200::1"}
	for _, s := range valid {
		if err := Validate(s); err != nil {
			t.Fatalf("Validate(%q) = %v", s, err)
		}
	}
	invalid := []string{"dns.google", "1.1.1.1:53", "999.1.1.1", "http://1.1.1.1"}
	for _, s := range invalid {
		if err := Validate(s); err == nil {
			t.Fatalf("Validate(%q) = nil, want error", s)
		}
	}
}
