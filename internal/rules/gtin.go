package rules

import "strings"

// GTINProblem describes why a barcode would be rejected as a GTIN.
type GTINProblem string

const (
	GTINOK            GTINProblem = ""
	GTINNotNumeric    GTINProblem = "contains characters other than digits"
	GTINBadLength     GTINProblem = "has the wrong length (GTINs are 8, 12, 13 or 14 digits)"
	GTINBadCheckDigit GTINProblem = "fails the check-digit test, so it is mistyped or made up"
	GTINRestricted    GTINProblem = "uses a restricted prefix (2, 02, 04) reserved for in-store codes"
	GTINAllSame       GTINProblem = "is a placeholder (all digits the same)"
)

// NormalizeGTIN strips the spaces and dashes merchants often paste in.
func NormalizeGTIN(s string) string {
	r := strings.NewReplacer(" ", "", "-", "", "\t", "")
	return r.Replace(strings.TrimSpace(s))
}

// CheckGTIN validates a barcode the way Merchant Center does: digits only,
// a GTIN length, a correct GS1 mod-10 check digit and no restricted prefix.
func CheckGTIN(raw string) GTINProblem {
	g := NormalizeGTIN(raw)
	for _, c := range g {
		if c < '0' || c > '9' {
			return GTINNotNumeric
		}
	}
	switch len(g) {
	case 8, 12, 13, 14:
	default:
		return GTINBadLength
	}
	if strings.Count(g, string(g[0])) == len(g) {
		return GTINAllSame
	}
	if !validCheckDigit(g) {
		return GTINBadCheckDigit
	}
	if len(g) <= 13 {
		padded := strings.Repeat("0", 13-len(g)) + g
		if strings.HasPrefix(padded, "2") || strings.HasPrefix(padded, "02") || strings.HasPrefix(padded, "04") {
			return GTINRestricted
		}
	}
	return GTINOK
}

// validCheckDigit implements the GS1 mod-10 algorithm: from the right,
// excluding the check digit, weights alternate 3,1,3,1...
func validCheckDigit(g string) bool {
	sum := 0
	body := g[:len(g)-1]
	for i := len(body) - 1; i >= 0; i-- {
		d := int(body[i] - '0')
		if (len(body)-1-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	check := (10 - sum%10) % 10
	return check == int(g[len(g)-1]-'0')
}
