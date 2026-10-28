package rules

import "testing"

func TestCheckGTIN(t *testing.T) {
	cases := []struct {
		in   string
		want GTINProblem
	}{
		{"4006381333931", GTINOK},   // EAN-13
		{"012345678905", GTINOK},    // UPC-A
		{"9780306406157", GTINOK},   // ISBN-13
		{"8901234567012", GTINOK},   // India (890) prefix
		{"8901 2345-67012", GTINOK}, // pasted with spaces and dashes
		{"96385074", GTINOK},        // EAN-8
		{"4006381333932", GTINBadCheckDigit},
		{"8901234567013", GTINBadCheckDigit},
		{"2001234567893", GTINRestricted}, // in-store prefix
		{"12345", GTINBadLength},
		{"ABC123456789", GTINNotNumeric},
		{"0000000000000", GTINAllSame},
	}
	for _, c := range cases {
		if got := CheckGTIN(c.in); got != c.want {
			t.Errorf("CheckGTIN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShouting(t *testing.T) {
	cases := map[string]bool{
		"HANDLOOM COTTON KURTA":         true,
		"Handloom Cotton Kurta":         false,
		"USB-C Charger 65W for MacBook": false, // short acronyms are fine
		"Kurta XL":                      false,
	}
	for in, want := range cases {
		if got := shouting(in); got != want {
			t.Errorf("shouting(%q) = %v, want %v", in, got, want)
		}
	}
}
