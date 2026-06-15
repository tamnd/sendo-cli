package sendo

import (
	"context"
	"strings"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

func init() { kit.Register(Domain{}) }

// Domain is the Sendo driver.
type Domain struct{}

func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "sendo",
		Hosts:  []string{Host},
		Identity: kit.Identity{
			Binary: "sendo",
			Short:  "Read public Sendo (sendo.vn) product listings.",
			Long: `Read public Sendo (sendo.vn) product listings.

sendo reads product details from the Sendo internal API and product listings
from category and search HTML pages — no API key, no browser required.
Returns clean JSON records ready for jq, sqlite-utils, and shell pipelines.`,
			Site: Host,
			Repo: "https://github.com/tamnd/sendo-cli",
		},
	}
}

func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	kit.Handle(app, kit.OpMeta{Name: "product", Group: "read", Single: true,
		URIType: "product", Resolver: true,
		Summary: "Fetch full details for a Sendo product by ID",
		Args:    []kit.Arg{{Name: "id", Help: "Sendo product ID"}}}, getProduct)

	kit.Handle(app, kit.OpMeta{Name: "products", Group: "read", List: true,
		URIType: "product",
		Summary: "List products from a Sendo category page",
		Args:    []kit.Arg{{Name: "slug", Help: "category slug (URL path segment)"}}}, listProducts)

	kit.Handle(app, kit.OpMeta{Name: "search", Group: "read", List: true,
		URIType: "product",
		Summary: "Search Sendo products by keyword",
		Args:    []kit.Arg{{Name: "query", Help: "search keyword"}}}, searchProducts)
}

func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := DefaultConfig()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.Timeout = cfg.Timeout
	}
	return NewClientWithConfig(c), nil
}

type productInput struct {
	ID     string  `kit:"arg"   help:"Sendo product ID"`
	Client *Client `kit:"inject"`
}

type productsInput struct {
	Slug   string  `kit:"arg"          help:"category slug"`
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

type searchInput struct {
	Query  string  `kit:"arg"          help:"search keyword"`
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

func getProduct(ctx context.Context, in productInput, emit func(*Product) error) error {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return errs.Usage("product id is required")
	}
	p, err := in.Client.GetProduct(ctx, id)
	if err != nil {
		return err
	}
	return emit(p)
}

func listProducts(ctx context.Context, in productsInput, emit func(*Product) error) error {
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		return errs.Usage("category slug is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	products, err := in.Client.CategoryProducts(ctx, slug, limit)
	if err != nil {
		return err
	}
	for _, p := range products {
		if err := emit(p); err != nil {
			return err
		}
	}
	return nil
}

func searchProducts(ctx context.Context, in searchInput, emit func(*Product) error) error {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return errs.Usage("search query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	products, err := in.Client.SearchProducts(ctx, query, limit)
	if err != nil {
		return err
	}
	for _, p := range products {
		if err := emit(p); err != nil {
			return err
		}
	}
	return nil
}

// Classify turns a Sendo URL or product ID into (type, id).
func (Domain) Classify(input string) (uriType, id string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errs.Usage("empty Sendo reference")
	}
	if strings.Contains(input, "sendo.vn/") {
		id = extractProductID(input)
		if id != "" {
			return "product", id, nil
		}
	}
	if isDigits(input) {
		return "product", input, nil
	}
	return "", "", errs.Usage("unrecognized Sendo reference: %q", input)
}

// Locate returns the live https URL for a (type, id).
func (Domain) Locate(uriType, id string) (string, error) {
	switch uriType {
	case "product":
		return baseURL + "/" + id + ".html", nil
	default:
		return "", errs.Usage("sendo has no resource type %q", uriType)
	}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
