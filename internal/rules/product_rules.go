package rules

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

// Merchant Center limits used by the rules.
const (
	maxTitleLen       = 150
	maxDescriptionLen = 5000
	minImageSide      = 100 // non-apparel minimum, in pixels
	minApparelSide    = 250 // apparel minimum, in pixels
)

// All returns every built-in rule in reporting order.
func All() []Rule {
	return []Rule{
		{ID: "store.policies", Summary: "Refund policy and contact details are published", Store: checkStorePolicies},
		{ID: "link.unpublished", Summary: "Product has a live product page", Product: checkLink},
		{ID: "title.missing", Summary: "Product has a title", Product: checkTitleMissing},
		{ID: "title.too_long", Summary: "Title fits in 150 characters", Product: checkTitleLength},
		{ID: "title.promotional", Summary: "Title has no promotional text or shouting", Product: checkTitlePromo},
		{ID: "description.missing", Summary: "Product has a description", Product: checkDescription},
		{ID: "image.missing", Summary: "Product has an image", Product: checkImageMissing},
		{ID: "image.too_small", Summary: "Main image meets Google's minimum size", Product: checkImageSize},
		{ID: "image.placeholder", Summary: "Main image is a real product photo", Product: checkImagePlaceholder},
		{ID: "price.invalid", Summary: "Every variant has a price above zero", Product: checkPrice},
		{ID: "price.compare_at", Summary: "Compare-at price is higher than the price", Product: checkCompareAt},
		{ID: "gtin.invalid", Summary: "Barcodes are valid GTINs", Product: checkGTINInvalid},
		{ID: "gtin.missing", Summary: "Branded products carry a GTIN", Product: checkGTINMissing},
		{ID: "brand.missing", Summary: "Product has a brand", Product: checkBrand},
		{ID: "category.missing", Summary: "Google product category is set", Product: checkCategory},
		{ID: "apparel.attributes", Summary: "Apparel has color, size, gender and age group", Product: checkApparel},
		{ID: "availability.oversell", Summary: "Out-of-stock variants are not sold as in stock", Product: checkOversell},
		{ID: "condition.mismatch", Summary: "Used or refurbished items declare their condition", Product: checkCondition},
		{ID: "shipping.weight", Summary: "Variants have a shipping weight", Product: checkWeight},
		{ID: "id.duplicate", Summary: "SKUs and barcodes are unique across the catalog", Store: checkDuplicates},
	}
}

// ---- links -----------------------------------------------------------------

func checkLink(_ *catalog.Store, p *catalog.Product) []Issue {
	// A password-protected store (every dev store, and live stores before
	// launch) has no public URL, so the publish date is the real signal.
	if strings.TrimSpace(p.OnlineStoreURL) != "" || strings.TrimSpace(p.PublishedAt) != "" {
		return nil
	}
	return []Issue{{
		Severity: Disapproval, Field: "link",
		Message: "Product is not published to the Online Store, so Google has no landing page to crawl.",
		Fix:     "Publish the product to the Online Store sales channel.",
	}}
}

// ---- title -----------------------------------------------------------------

func checkTitleMissing(_ *catalog.Store, p *catalog.Product) []Issue {
	if strings.TrimSpace(p.Title) != "" {
		return nil
	}
	return []Issue{{
		Severity: Disapproval, Field: "title",
		Message: "Title is empty.",
		Fix:     "Add a title in the pattern Brand + Product + key attribute (size, color, material).",
	}}
}

func checkTitleLength(_ *catalog.Store, p *catalog.Product) []Issue {
	n := len([]rune(strings.TrimSpace(p.Title)))
	if n <= maxTitleLen {
		return nil
	}
	return []Issue{{
		Severity: Warning, Field: "title",
		Message:     fmt.Sprintf("Title is %d characters; Google allows %d and cuts the rest.", n, maxTitleLen),
		Fix:         "Shorten the title and put the most important words in the first 70 characters.",
		AutoFixable: true,
	}}
}

var promoPattern = regexp.MustCompile(`(?i)\b(free shipping|free delivery|sale|on sale|\d+\s?% off|discount|best price|cheapest|limited time|buy now|clearance|deal of the day)\b`)

func checkTitlePromo(_ *catalog.Store, p *catalog.Product) []Issue {
	var out []Issue
	if m := promoPattern.FindString(p.Title); m != "" {
		out = append(out, Issue{
			Severity: Disapproval, Field: "title",
			Message:     fmt.Sprintf("Title contains promotional text (%q), which breaks Google's editorial standards.", m),
			Fix:         "Remove promotional wording from the title. Use Merchant Center promotions or the sale price for offers.",
			AutoFixable: true,
		})
	}
	if shouting(p.Title) {
		out = append(out, Issue{
			Severity: Warning, Field: "title",
			Message:     "Title is mostly capital letters, which Google treats as excessive capitalization.",
			Fix:         "Use normal title case.",
			AutoFixable: true,
		})
	}
	return out
}

// shouting reports whether more than half the words of 4+ letters are fully
// uppercase. Short acronyms like "USB" or "XL" are ignored.
func shouting(title string) bool {
	words := strings.Fields(title)
	long, caps := 0, 0
	for _, w := range words {
		letters := 0
		upper := 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
		if letters < 4 {
			continue
		}
		long++
		if upper == letters {
			caps++
		}
	}
	return long >= 2 && caps*2 > long
}

// ---- description -----------------------------------------------------------

var tagPattern = regexp.MustCompile(`<[^>]*>`)

// PlainText strips HTML tags and entities from a Shopify description.
func PlainText(h string) string {
	t := tagPattern.ReplaceAllString(h, " ")
	t = html.UnescapeString(t)
	return strings.Join(strings.Fields(t), " ")
}

func checkDescription(_ *catalog.Store, p *catalog.Product) []Issue {
	text := PlainText(p.DescriptionHTML)
	n := len([]rune(text))
	switch {
	case n == 0:
		return []Issue{{
			Severity: Disapproval, Field: "description",
			Message:     "Description is empty.",
			Fix:         "Write a description covering what the product is, materials, dimensions and who it is for.",
			AutoFixable: true,
		}}
	case n < 30:
		return []Issue{{
			Severity: Warning, Field: "description",
			Message:     fmt.Sprintf("Description is only %d characters, too thin for Google or AI shopping assistants to match queries against.", n),
			Fix:         "Expand the description with materials, dimensions, use cases and key specs.",
			AutoFixable: true,
		}}
	case n > maxDescriptionLen:
		return []Issue{{
			Severity: Warning, Field: "description",
			Message:     fmt.Sprintf("Description is %d characters; Google allows %d.", n, maxDescriptionLen),
			Fix:         "Trim the description, keeping specs and key details near the top.",
			AutoFixable: true,
		}}
	}
	return nil
}

// ---- images ----------------------------------------------------------------

func checkImageMissing(_ *catalog.Store, p *catalog.Product) []Issue {
	if len(p.Images) > 0 && strings.TrimSpace(p.Images[0].URL) != "" {
		return nil
	}
	return []Issue{{
		Severity: Disapproval, Field: "image_link",
		Message: "Product has no image.",
		Fix:     "Upload at least one clear photo of the product on a plain background.",
	}}
}

func checkImageSize(_ *catalog.Store, p *catalog.Product) []Issue {
	if len(p.Images) == 0 {
		return nil
	}
	img := p.Images[0]
	if img.Width == 0 || img.Height == 0 {
		return nil // dimensions unknown; the live scan fetches them
	}
	min := minImageSide
	if isApparel(p) {
		min = minApparelSide
	}
	if img.Width >= min && img.Height >= min {
		return nil
	}
	return []Issue{{
		Severity: Disapproval, Field: "image_link",
		Message: fmt.Sprintf("Main image is %dx%d px; Google requires at least %dx%d for this kind of product.", img.Width, img.Height, min, min),
		Fix:     "Replace it with an image of at least 800x800 px.",
	}}
}

var placeholderPattern = regexp.MustCompile(`(?i)(no[-_]?image|placeholder|coming[-_]?soon|default[-_]?product)`)

func checkImagePlaceholder(_ *catalog.Store, p *catalog.Product) []Issue {
	if len(p.Images) == 0 {
		return nil
	}
	if !placeholderPattern.MatchString(p.Images[0].URL) {
		return nil
	}
	return []Issue{{
		Severity: Disapproval, Field: "image_link",
		Message: "Main image looks like a placeholder, which Google rejects as a generic image.",
		Fix:     "Replace it with a real photo of the product.",
	}}
}

// ---- price -----------------------------------------------------------------

func checkPrice(_ *catalog.Store, p *catalog.Product) []Issue {
	var out []Issue
	for _, v := range p.Variants {
		price, ok := catalog.Money(v.Price)
		if ok && price > 0 {
			continue
		}
		out = append(out, Issue{
			Severity: Disapproval, Field: "price", VariantID: v.ID,
			Message: fmt.Sprintf("Variant %q has no valid price (%q).", variantName(v), v.Price),
			Fix:     "Set a price above zero.",
		})
	}
	return out
}

func checkCompareAt(_ *catalog.Store, p *catalog.Product) []Issue {
	var out []Issue
	for _, v := range p.Variants {
		cmp, ok := catalog.Money(v.CompareAtPrice)
		if !ok || cmp == 0 {
			continue
		}
		price, _ := catalog.Money(v.Price)
		if cmp > price {
			continue
		}
		out = append(out, Issue{
			Severity: Warning, Field: "sale_price", VariantID: v.ID,
			Message:     fmt.Sprintf("Variant %q has compare-at %.2f at or below its price %.2f, so the feed sends a sale price that is not a discount.", variantName(v), cmp, price),
			Fix:         "Clear the compare-at price, or set it above the current price.",
			AutoFixable: true,
		})
	}
	return out
}

// ---- identifiers -----------------------------------------------------------

func checkGTINInvalid(_ *catalog.Store, p *catalog.Product) []Issue {
	var out []Issue
	for _, v := range p.Variants {
		if strings.TrimSpace(v.Barcode) == "" {
			continue
		}
		if prob := CheckGTIN(v.Barcode); prob != GTINOK {
			out = append(out, Issue{
				Severity: Disapproval, Field: "gtin", VariantID: v.ID,
				Message: fmt.Sprintf("Barcode %q on variant %q %s.", v.Barcode, variantName(v), prob),
				Fix:     "Copy the GTIN from the product packaging or the manufacturer. If the product has none, clear the barcode field.",
			})
		}
	}
	return out
}

func checkGTINMissing(st *catalog.Store, p *catalog.Product) []Issue {
	missing := 0
	for _, v := range p.Variants {
		if strings.TrimSpace(v.Barcode) == "" {
			missing++
		}
	}
	if missing == 0 || len(p.Variants) == 0 || strings.TrimSpace(p.Vendor) == "" {
		return nil // with no vendor, brand.missing is the issue to fix first
	}
	if ownBrand(st, p) {
		return []Issue{{
			Severity: Opportunity, Field: "identifier_exists",
			Message:     fmt.Sprintf("%d variant(s) have no GTIN. This looks like your own brand, so Google needs to be told no GTIN exists.", missing),
			Fix:         "Send identifier_exists = no for these variants so Google treats them as custom products.",
			AutoFixable: true,
		}}
	}
	return []Issue{{
		Severity: Warning, Field: "gtin",
		Message: fmt.Sprintf("%d variant(s) of a %q product have no GTIN. Google limits reach for branded products without one.", missing, p.Vendor),
		Fix:     "Add the manufacturer's barcode (UPC, EAN or ISBN) to each variant.",
	}}
}

func checkBrand(_ *catalog.Store, p *catalog.Product) []Issue {
	if strings.TrimSpace(p.Vendor) != "" {
		return nil
	}
	return []Issue{{
		Severity: Warning, Field: "brand",
		Message:     "Product has no vendor, so the feed sends no brand.",
		Fix:         "Set the vendor to the manufacturer's brand, or your store name for your own products.",
		AutoFixable: true,
	}}
}

// ---- category and apparel --------------------------------------------------

func checkCategory(_ *catalog.Store, p *catalog.Product) []Issue {
	if strings.TrimSpace(p.GoogleCategory) != "" {
		return nil
	}
	return []Issue{{
		Severity: Opportunity, Field: "google_product_category",
		Message:     "No Google product category is set, so Google guesses one and may apply the wrong attribute requirements.",
		Fix:         "Set the Google product category from Google's taxonomy.",
		AutoFixable: true,
	}}
}

var apparelWords = regexp.MustCompile(`(?i)\b(apparel|clothing|shirt|t-shirt|tee|dress|jeans|pants|trousers|jacket|hoodie|sweater|skirt|shoes?|sneakers?|boots?|kurta|saree)\b`)

func isApparel(p *catalog.Product) bool {
	if strings.Contains(strings.ToLower(p.GoogleCategory), "apparel") {
		return true
	}
	return apparelWords.MatchString(p.ProductType) || apparelWords.MatchString(p.Title)
}

// needsSize reports whether Google requires a size: clothing and shoes do,
// bags, wallets and other accessories do not.
func needsSize(p *catalog.Product) bool {
	cat := strings.ToLower(p.GoogleCategory)
	if cat != "" {
		return strings.Contains(cat, "clothing") || strings.Contains(cat, "shoes")
	}
	return apparelWords.MatchString(p.ProductType) || apparelWords.MatchString(p.Title)
}

func checkApparel(_ *catalog.Store, p *catalog.Product) []Issue {
	if !isApparel(p) {
		return nil
	}
	var missing []string
	if p.Gender == "" {
		missing = append(missing, "gender")
	}
	if p.AgeGroup == "" {
		missing = append(missing, "age group")
	}
	var noColor, noSize int
	for _, v := range p.Variants {
		if v.Option("color") == "" && v.Option("colour") == "" {
			noColor++
		}
		if v.Option("size") == "" {
			noSize++
		}
	}
	if noColor > 0 {
		missing = append(missing, fmt.Sprintf("color (%d variants)", noColor))
	}
	if noSize > 0 && needsSize(p) {
		missing = append(missing, fmt.Sprintf("size (%d variants)", noSize))
	}
	if len(missing) == 0 {
		return nil
	}
	return []Issue{{
		Severity: Disapproval, Field: "apparel attributes",
		Message:     "Apparel product is missing required attributes: " + strings.Join(missing, ", ") + ".",
		Fix:         "Add Color and Size variant options, and set gender and age group in the Google & YouTube channel or product metafields.",
		AutoFixable: true,
	}}
}

// ---- availability ----------------------------------------------------------

func checkOversell(_ *catalog.Store, p *catalog.Product) []Issue {
	var out []Issue
	for _, v := range p.Variants {
		if !v.InventoryTracked || v.InventoryQuantity > 0 {
			continue
		}
		if !strings.EqualFold(v.InventoryPolicy, "CONTINUE") {
			continue
		}
		out = append(out, Issue{
			Severity: Warning, Field: "availability", VariantID: v.ID,
			Message: fmt.Sprintf("Variant %q has %d in stock but keeps selling, so it is sent as in stock. If the page shows a delay, Google flags an availability mismatch.", variantName(v), v.InventoryQuantity),
			Fix:     "Send availability = backorder with an availability date, or stop selling when out of stock.",
		})
	}
	return out
}

// ---- condition and shipping ------------------------------------------------

var usedWords = regexp.MustCompile(`(?i)\b(used|refurbished|renewed|pre-?owned|second[- ]hand|open box)\b`)

func checkCondition(_ *catalog.Store, p *catalog.Product) []Issue {
	m := usedWords.FindString(p.Title)
	if m == "" {
		return nil
	}
	c := strings.ToLower(p.Condition)
	if c == "used" || c == "refurbished" {
		return nil
	}
	return []Issue{{
		Severity: Disapproval, Field: "condition",
		Message:     fmt.Sprintf("Title says %q but condition is %q, which Google treats as misrepresenting the product.", m, orDefault(p.Condition, "new (default)")),
		Fix:         "Set condition to used or refurbished.",
		AutoFixable: true,
	}}
}

func checkWeight(_ *catalog.Store, p *catalog.Product) []Issue {
	missing := 0
	for _, v := range p.Variants {
		if v.Weight <= 0 {
			missing++
		}
	}
	if missing == 0 {
		return nil
	}
	return []Issue{{
		Severity: Opportunity, Field: "shipping_weight",
		Message: fmt.Sprintf("%d variant(s) have no weight. Google cannot calculate weight-based shipping costs for them.", missing),
		Fix:     "Add the packed weight to each variant.",
	}}
}

// ---- helpers ---------------------------------------------------------------

func variantName(v catalog.Variant) string {
	if v.Title != "" && v.Title != "Default Title" {
		return v.Title
	}
	if v.SKU != "" {
		return v.SKU
	}
	return v.ID
}

func ownBrand(st *catalog.Store, p *catalog.Product) bool {
	return st != nil && st.Name != "" && strings.EqualFold(strings.TrimSpace(p.Vendor), strings.TrimSpace(st.Name))
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
