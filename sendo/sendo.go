// Package sendo is the library behind the sendo command line:
// the HTTP client, API parsing, and typed data models for Sendo
// (sendo.vn), a major Vietnamese e-commerce marketplace.
//
// Sendo exposes an internal JSON API for product details. Listing pages
// and search results are parsed from HTML via JSON-LD or regex extraction.
// Product URLs follow the pattern: https://www.sendo.vn/{slug}-{id}.html.
package sendo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Host is the canonical site hostname.
const Host = "sendo.vn"

// baseURL is the site root with www prefix (required for API).
const baseURL = "https://www.sendo.vn"

// DefaultUserAgent mimics a real browser to avoid Cloudflare blocks.
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// Config holds the tunable knobs for the HTTP client.
type Config struct {
	BaseURL   string
	Rate      time.Duration
	Retries   int
	Timeout   time.Duration
	UserAgent string
}

// DefaultConfig returns sensible production defaults.
func DefaultConfig() Config {
	return Config{
		BaseURL:   baseURL,
		Rate:      2 * time.Second,
		Retries:   3,
		Timeout:   30 * time.Second,
		UserAgent: DefaultUserAgent,
	}
}

// Client talks to the Sendo website over HTTP.
type Client struct {
	cfg  Config
	http *http.Client
	last time.Time
}

// NewClient returns a Client from DefaultConfig.
func NewClient() *Client { return NewClientWithConfig(DefaultConfig()) }

// NewClientWithConfig returns a Client built from cfg.
func NewClientWithConfig(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

// Get fetches rawURL and returns the body bytes, pacing and retrying on transient errors.
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) ([]byte, bool, error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/json,*/*")
	req.Header.Set("Referer", baseURL+"/")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	return b, err != nil, err
}

func (c *Client) pace() {
	if c.cfg.Rate <= 0 {
		return
	}
	if wait := c.cfg.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// --- wire types (Sendo internal API) ---

type wireDetailResp struct {
	Result wireProduct `json:"result"`
}

type wireProduct struct {
	ID              int64          `json:"id"`
	Name            string         `json:"name"`
	Price           float64        `json:"price"`
	OriginalPrice   float64        `json:"original_price"`
	DiscountPercent int            `json:"discount_percent"`
	Rating          float64        `json:"rating"`
	ReviewCount     int            `json:"review_count"`
	SoldCount       int64          `json:"order_count"`
	IsAuthentic     bool           `json:"is_authentic"`
	IsFreeShip      bool           `json:"is_freeship"`
	Location        string         `json:"location"`
	Seller          wireSeller     `json:"shop"`
	Images          []string       `json:"images"`
	Attributes      []wireAttr     `json:"attributes"`
	CategoryPath    []wireCatCrumb `json:"category"`
}

type wireSeller struct {
	ID     int64   `json:"id"`
	Name   string  `json:"shop_name"`
	Rating float64 `json:"rating"`
}

type wireAttr struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type wireCatCrumb struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// wireListingProduct is extracted from listing page JSON-LD or JSON blobs.
type wireListingProduct struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

// --- public types ---

// Product is one Sendo product fetched from the internal API.
type Product struct {
	ID              string  `json:"id"                         kit:"id" table:"id"`
	Name            string  `json:"name"                                table:"name"`
	URL             string  `json:"url,omitempty"                       table:"url,url"`
	Price           float64 `json:"price"                               table:"price"`
	OriginalPrice   float64 `json:"original_price,omitempty"            table:"original_price"`
	DiscountPercent int     `json:"discount_percent,omitempty"          table:"discount_percent"`
	SellerName      string  `json:"seller_name,omitempty"               table:"seller_name"`
	SellerRating    float64 `json:"seller_rating,omitempty"             table:"seller_rating"`
	Rating          float64 `json:"rating,omitempty"                    table:"rating"`
	ReviewCount     int     `json:"review_count,omitempty"              table:"reviews"`
	SoldCount       int64   `json:"sold_count,omitempty"                table:"sold"`
	IsAuthentic     bool    `json:"is_authentic,omitempty"              table:"authentic"`
	IsFreeShip      bool    `json:"is_freeship,omitempty"               table:"freeship"`
	Location        string  `json:"location,omitempty"                  table:"location"`
	FetchedAt       string  `json:"fetched_at,omitempty"                table:"fetched_at"`
}

// --- client methods ---

// GetProduct fetches full details for a single product by numeric ID.
func (c *Client) GetProduct(ctx context.Context, id string) (*Product, error) {
	base := c.cfg.BaseURL
	if base == "" {
		base = baseURL
	}
	apiURL := base + "/api/v2/product/detail/" + id
	body, err := c.Get(ctx, apiURL)
	if err != nil {
		return nil, fmt.Errorf("product %s: %w", id, err)
	}

	var resp wireDetailResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode product %s: %w", id, err)
	}
	if resp.Result.ID == 0 {
		return nil, fmt.Errorf("product %s: not found in response", id)
	}
	return productFromWire(resp.Result, base), nil
}

// SearchProducts fetches products matching a search query from the listing HTML.
func (c *Client) SearchProducts(ctx context.Context, query string, limit int) ([]*Product, error) {
	if limit <= 0 {
		limit = 20
	}
	base := c.cfg.BaseURL
	if base == "" {
		base = baseURL
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("page", "1")
	pageURL := base + "/ket-qua-tim-kiem/?" + params.Encode()

	body, err := c.Get(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}
	return parseListingHTML(body, limit, base), nil
}

// CategoryProducts fetches products from a category listing page.
func (c *Client) CategoryProducts(ctx context.Context, slug string, limit int) ([]*Product, error) {
	if limit <= 0 {
		limit = 20
	}
	base := c.cfg.BaseURL
	if base == "" {
		base = baseURL
	}
	pageURL := base + "/" + slug + "/"
	body, err := c.Get(ctx, pageURL)
	if err != nil {
		return nil, fmt.Errorf("category %s: %w", slug, err)
	}
	return parseListingHTML(body, limit, base), nil
}

// --- HTML parsing ---

// productLinkRE finds product links in Sendo listing HTML.
// Pattern: href="/something-123456789.html" where the trailing digits are the product ID.
var productLinkRE = regexp.MustCompile(`href="(?:https://www\.sendo\.vn)?(/[^"]+?-(\d{7,})\.html)"`)

// jsonLdRE finds a JSON-LD Product block in HTML.
var jsonLdRE = regexp.MustCompile(`(?is)<script[^>]+type="application/ld\+json"[^>]*>([\s\S]*?)</script>`)

// priceRE finds a price in a JSON-LD Product block.
var priceRE = regexp.MustCompile(`"price"\s*:\s*"?([\d.]+)"?`)

// namePropRE finds the name in a JSON-LD Product block.
var namePropRE = regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)

func parseListingHTML(body []byte, limit int, base string) []*Product {
	html := string(body)
	// First try JSON-LD blocks for structured product data.
	products := parseFromJSONLD(html, limit, base)
	if len(products) > 0 {
		return products
	}
	// Fall back to link extraction.
	return parseFromLinks(html, limit, base)
}

func parseFromJSONLD(html string, limit int, base string) []*Product {
	matches := jsonLdRE.FindAllStringSubmatch(html, -1)
	var out []*Product
	seen := map[string]bool{}

	for _, m := range matches {
		if len(out) >= limit {
			break
		}
		block := m[1]
		if !strings.Contains(block, `"Product"`) {
			continue
		}
		// Extract product ID from URL in JSON-LD.
		urlM := productLinkRE.FindStringSubmatch(block)
		if len(urlM) < 3 {
			continue
		}
		id := urlM[2]
		if seen[id] {
			continue
		}
		seen[id] = true

		name := ""
		if nm := namePropRE.FindStringSubmatch(block); len(nm) >= 2 {
			name = strings.ReplaceAll(nm[1], `\"`, `"`)
		}
		price := 0.0
		if pm := priceRE.FindStringSubmatch(block); len(pm) >= 2 {
			price, _ = strconv.ParseFloat(pm[1], 64)
		}

		out = append(out, &Product{
			ID:        id,
			Name:      name,
			URL:       base + urlM[1],
			Price:     price,
			FetchedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return out
}

func parseFromLinks(html string, limit int, base string) []*Product {
	matches := productLinkRE.FindAllStringSubmatch(html, -1)
	seen := map[string]bool{}
	var out []*Product

	for _, m := range matches {
		if len(out) >= limit {
			break
		}
		if len(m) < 3 {
			continue
		}
		id := m[2]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, &Product{
			ID:        id,
			URL:       base + m[1],
			FetchedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return out
}

// productFromWire converts the internal API response to a public Product.
func productFromWire(w wireProduct, base string) *Product {
	if base == "" {
		base = baseURL
	}
	productURL := base + "/" + strconv.FormatInt(w.ID, 10) + ".html"
	return &Product{
		ID:              strconv.FormatInt(w.ID, 10),
		Name:            w.Name,
		URL:             productURL,
		Price:           w.Price,
		OriginalPrice:   w.OriginalPrice,
		DiscountPercent: w.DiscountPercent,
		SellerName:      w.Seller.Name,
		SellerRating:    w.Seller.Rating,
		Rating:          w.Rating,
		ReviewCount:     w.ReviewCount,
		SoldCount:       w.SoldCount,
		IsAuthentic:     w.IsAuthentic,
		IsFreeShip:      w.IsFreeShip,
		Location:        w.Location,
		FetchedAt:       time.Now().UTC().Format(time.RFC3339),
	}
}

// productIDRE extracts the trailing numeric ID from a Sendo product URL.
// Pattern: /{slug}-{id}.html where id is 7+ digits.
var productIDRE = regexp.MustCompile(`-(\d{7,})\.html`)

// extractProductID extracts the numeric product ID from a Sendo URL.
func extractProductID(rawURL string) string {
	m := productIDRE.FindStringSubmatch(rawURL)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
