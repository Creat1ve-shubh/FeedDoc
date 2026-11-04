package rules

import (
	"strings"
	"testing"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

func TestCleanTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"FREE SHIPPING!! HANDLOOM COTTON KURTA BEST PRICE", "Handloom Cotton Kurta"},
		{"Organic Tote Bag - 20% OFF", "Organic Tote Bag"},
		{"LED DESK LAMP AND USB CHARGER", "LED Desk Lamp and USB Charger"},
		{"Blue Pottery Ceramic Coffee Mug", "Blue Pottery Ceramic Coffee Mug"},
		{"Sale", "Sale"}, // never blank a title
	}
	for _, c := range cases {
		if got := CleanTitle(c.in); got != c.want {
			t.Errorf("CleanTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanTitleCutsAtWordBoundary(t *testing.T) {
	long := strings.Repeat("Handmade ", 30) + "Mug"
	got := CleanTitle(long)
	if n := len([]rune(got)); n > maxTitleLen {
		t.Fatalf("got %d characters, want at most %d", n, maxTitleLen)
	}
	if strings.HasSuffix(got, " ") || !strings.HasSuffix(got, "Handmade") {
		t.Fatalf("cut mid-word: %q", got)
	}
}

func TestCleanTitleClearsTheRules(t *testing.T) {
	for _, in := range []string{
		"FREE SHIPPING!! HANDLOOM COTTON KURTA BEST PRICE",
		"Clearance: Linen Shirt | Buy Now",
		strings.Repeat("Very Long Title ", 20),
	} {
		p := productWithTitle(CleanTitle(in))
		for _, is := range append(checkTitlePromo(nil, p), checkTitleLength(nil, p)...) {
			t.Errorf("CleanTitle(%q) = %q still has %s: %s", in, p.Title, is.RuleID, is.Message)
		}
	}
}

func productWithTitle(title string) *catalog.Product {
	return &catalog.Product{Title: title}
}
