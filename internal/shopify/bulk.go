package shopify

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Creat1ve-shubh/FeedDoc/internal/catalog"
)

// BulkProductsQuery exports every product with its media and variants. A bulk
// operation flattens nested connections into separate JSONL lines that point
// at their parent with __parentId, so ParseBulkProducts stitches them back.
var BulkProductsQuery = `{
  products {
    edges { node {` + productFields + `
      media { edges { node {` + mediaFields + `
      } } }
      variants { edges { node {` + variantFields + `
      } } }
    } }
  }
}`

type bulkLine struct {
	ID       string `json:"id"`
	ParentID string `json:"__parentId"`
}

// ParseBulkProducts reads a bulk operation's JSONL result into products, in
// export order. Children always follow their parent in the file.
func ParseBulkProducts(r io.Reader) ([]catalog.Product, error) {
	var products []catalog.Product
	index := map[string]int{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // descriptions can be long
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var head bulkLine
		if err := json.Unmarshal(raw, &head); err != nil {
			return nil, fmt.Errorf("bulk line %d: %w", line, err)
		}
		if head.ParentID == "" {
			var p gqlProduct
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, fmt.Errorf("bulk line %d: %w", line, err)
			}
			index[p.ID] = len(products)
			products = append(products, p.toCatalog())
			continue
		}
		i, ok := index[head.ParentID]
		if !ok {
			return nil, fmt.Errorf("bulk line %d: parent %s not seen yet", line, head.ParentID)
		}
		parent := &products[i]
		switch gidType(head.ID) {
		case "ProductVariant":
			var v gqlVariant
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, fmt.Errorf("bulk line %d: %w", line, err)
			}
			parent.Variants = append(parent.Variants, v.toCatalog())
		default:
			// MediaImage, or a non-image media line with no fields selected.
			var m gqlMedia
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, fmt.Errorf("bulk line %d: %w", line, err)
			}
			addMedia(parent, m)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read bulk result: %w", err)
	}
	return products, nil
}

// gidType returns "Product" for "gid://shopify/Product/123".
func gidType(gid string) string {
	rest, ok := strings.CutPrefix(gid, "gid://shopify/")
	if !ok {
		return ""
	}
	t, _, _ := strings.Cut(rest, "/")
	return t
}

// ProductGID turns a REST numeric id from a webhook into a GraphQL id.
func ProductGID(id int64) string { return fmt.Sprintf("gid://shopify/Product/%d", id) }
